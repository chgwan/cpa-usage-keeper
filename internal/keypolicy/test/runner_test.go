package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync"
	"testing"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/repository"

	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

// fakeCPAAPIKeys 复制自 service 测试包的内存 CPA：test 包之间不能共享 helper，复制是本仓库惯例。
// 额外的 deleteFail / onDelete 只服务 runner 的 CPA 写失败路径。
type fakeCPAAPIKeys struct {
	keys []string
	// deleteFail 为 true 时 DELETE 返回 500，模拟 CPA 写入故障。
	deleteFail bool
	// onDelete 在 DELETE 处理入口回调，用来快照调用瞬间数据库里的策略状态。
	onDelete func()
	// aroundGet 包裹 GET 的响应写出，用来观测并发或在写出前后阻塞请求。
	aroundGet func(serve func())
}

func (f *fakeCPAAPIKeys) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			serve := func() { _ = json.NewEncoder(w).Encode(map[string]any{"api-keys": f.keys}) }
			if f.aroundGet != nil {
				f.aroundGet(serve)
				return
			}
			serve()
		case http.MethodPut:
			var body struct {
				Items []string `json:"items"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode put body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// 空 items 的 PUT 会清空 CPA 全部 key，runner 永远不允许发出。
			if len(body.Items) == 0 {
				t.Errorf("must never PUT an empty key list")
			}
			f.keys = body.Items
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case http.MethodDelete:
			if f.onDelete != nil {
				f.onDelete()
			}
			if f.deleteFail {
				w.WriteHeader(http.StatusInternalServerError)
				return
			}
			value := r.URL.Query().Get("value")
			for i, key := range f.keys {
				if key == value {
					f.keys = append(f.keys[:i], f.keys[i+1:]...)
					_, _ = w.Write([]byte(`{"status":"ok"}`))
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}
}

// startFakeCPA 把内存 CPA 挂到本地 httptest 服务上。
func startFakeCPA(t *testing.T, fake *fakeCPAAPIKeys) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	return server
}

// newCPAClient 用与管理服务测试相同的参数构造真实 CPA client。
func newCPAClient(t *testing.T, server *httptest.Server) *cpa.Client {
	t.Helper()
	return cpa.NewClient(server.URL, "mgmt", 5*time.Second, false)
}

// fmt64 把浮点限额格式化成不含科学计数法的十进制文本。
func fmt64(value float64) string {
	return strconv.FormatFloat(value, 'f', -1, 64)
}

// seedLimitedKey 建一个带日限额 token 上限的 key，并灌入一条当日事件。
func seedLimitedKey(t *testing.T, db *gorm.DB, key string, limitValue float64, tokens int64) int64 {
	t.Helper()
	row := entities.CPAAPIKey{APIKey: key, DisplayKey: key}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	encoded := `[{"type":"tokens","window":"daily","value":` + fmt64(limitValue) + `}]`
	if err := repository.UpsertCPAAPIKeyPolicy(db, &entities.CPAAPIKeyPolicy{
		CPAAPIKeyID: row.ID, Limits: encoded, Enabled: true, EnforcementState: string(keypolicy.StateActive),
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	// 事件固定在当日正午：临近零点运行测试时 time.Now() 可能先一步落到昨天的日窗口。
	now := time.Now()
	noon := time.Date(now.Year(), now.Month(), now.Day(), 12, 0, 0, 0, now.Location())
	event := entities.UsageEvent{APIGroupKey: key, Model: "m", InputTokens: tokens, TotalTokens: tokens, Timestamp: noon}
	if err := db.Create(&event).Error; err != nil {
		t.Fatalf("seed event: %v", err)
	}
	return row.ID
}

func TestRunnerDisablesKeyOnBreachAndRestoresAfterWindowFlip(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-limit", "sk-other"}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	keyID := seedLimitedKey(t, db, "sk-limit", 100, 150)
	// 另一个本地 key 保证评估上下文里不只有超限 key 一行。
	other := entities.CPAAPIKey{APIKey: "sk-other", DisplayKey: "sk-other"}
	if err := db.Create(&other).Error; err != nil {
		t.Fatalf("seed other key: %v", err)
	}
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), nil)
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(fake.keys) != 1 || fake.keys[0] != "sk-other" {
		t.Fatalf("expected breached key removed from CPA, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, keyID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateDisabledByQuota) {
		t.Fatalf("expected disabled_by_quota state, got %+v err %v", policy, err)
	}
	logs, _ := repository.ListAPIKeyEnforcementLogs(db, keyID, 10)
	if len(logs) == 0 || logs[0].Action != "disabled" || logs[0].Reason != "limit_breached" {
		t.Fatalf("expected disabled audit log, got %+v", logs)
	}

	// 窗口翻转：删掉当日事件并补一条昨天的事件，再次评估应自动恢复。
	if err := db.Where("api_group_key = ?", "sk-limit").Delete(&entities.UsageEvent{}).Error; err != nil {
		t.Fatalf("clear events: %v", err)
	}
	yesterday := time.Now().AddDate(0, 0, -1)
	aged := entities.UsageEvent{
		APIGroupKey: "sk-limit", Model: "m", InputTokens: 150, TotalTokens: 150,
		Timestamp: time.Date(yesterday.Year(), yesterday.Month(), yesterday.Day(), 12, 0, 0, 0, yesterday.Location()),
	}
	if err := db.Create(&aged).Error; err != nil {
		t.Fatalf("seed aged event: %v", err)
	}
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate again: %v", err)
	}
	if len(fake.keys) != 2 {
		t.Fatalf("expected key restored to CPA, got %v", fake.keys)
	}
	policy, err = repository.FindCPAAPIKeyPolicy(db, keyID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateActive) {
		t.Fatalf("expected active state after restore, got %+v err %v", policy, err)
	}
}

func TestRunnerNeverDisablesLastKey(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-only"}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	seedLimitedKey(t, db, "sk-only", 100, 500)
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), nil)
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(fake.keys) != 1 || fake.keys[0] != "sk-only" {
		t.Fatalf("last key must never be removed, got %v", fake.keys)
	}
	policies, _ := repository.ListEnabledCPAAPIKeyPolicies(db)
	if len(policies) != 1 || policies[0].EnforcementState != string(keypolicy.StateActive) {
		t.Fatalf("expected state to stay active, got %+v", policies)
	}
	logs, _ := repository.ListAPIKeyEnforcementLogs(db, policies[0].CPAAPIKeyID, 10)
	if len(logs) == 0 || logs[0].Action != "skipped_last_key" {
		t.Fatalf("expected skipped_last_key audit, got %+v", logs)
	}
}

func TestRunnerKeepsManualDisableAcrossEvaluations(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	keyID := seedLimitedKey(t, db, "sk-manual", 100, 0)
	if err := repository.UpsertCPAAPIKeyPolicy(db, &entities.CPAAPIKeyPolicy{
		CPAAPIKeyID: keyID, Limits: "[]", Enabled: true, AdminDisabled: true,
		EnforcementState: string(keypolicy.StateDisabledManual),
	}); err != nil {
		t.Fatalf("set manual state: %v", err)
	}
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), nil)
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(fake.keys) != 0 {
		t.Fatalf("manual-disabled key must not be auto-restored, got %v", fake.keys)
	}
}

// TestRunnerWritesDisabledStateBeforeCPADeleteAndRetriesAfterFailure 验证 TOCTOU 约束的裁决顺序：
// 状态先于 CPA DELETE 落库；DELETE 失败时状态回滚 active，让下一轮重试删除。
func TestRunnerWritesDisabledStateBeforeCPADeleteAndRetriesAfterFailure(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-flaky", "sk-buddy"}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	keyID := seedLimitedKey(t, db, "sk-flaky", 100, 150)

	// DELETE 入口快照策略状态：状态必须已经在 DELETE 之前翻成 disabled_by_quota。
	statesAtDelete := make([]string, 0, 2)
	fake.onDelete = func() {
		policy, err := repository.FindCPAAPIKeyPolicy(db, keyID)
		if err != nil {
			t.Errorf("snapshot policy at delete time: %v", err)
			return
		}
		statesAtDelete = append(statesAtDelete, policy.EnforcementState)
	}
	fake.deleteFail = true
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), nil)
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate with failing delete: %v", err)
	}
	if len(statesAtDelete) != 1 || statesAtDelete[0] != string(keypolicy.StateDisabledByQuota) {
		t.Fatalf("expected disabled_by_quota state at delete time, got %v", statesAtDelete)
	}
	// DELETE 失败：key 留在 CPA，状态回滚 active，审计落 failed/retry。
	if len(fake.keys) != 2 {
		t.Fatalf("expected key kept in CPA after failed delete, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, keyID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateActive) {
		t.Fatalf("expected state reverted to active, got %+v err %v", policy, err)
	}
	logs, _ := repository.ListAPIKeyEnforcementLogs(db, keyID, 10)
	if len(logs) == 0 || logs[0].Action != "failed" || logs[0].Reason != "retry" {
		t.Fatalf("expected failed/retry audit, got %+v", logs)
	}

	// 下一轮 DELETE 恢复后必须重试并最终禁用。
	fake.deleteFail = false
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate retry: %v", err)
	}
	if len(statesAtDelete) != 2 {
		t.Fatalf("expected delete retried on second tick, got %d calls", len(statesAtDelete))
	}
	if len(fake.keys) != 1 || fake.keys[0] != "sk-buddy" {
		t.Fatalf("expected breached key removed on retry, got %v", fake.keys)
	}
	policy, err = repository.FindCPAAPIKeyPolicy(db, keyID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateDisabledByQuota) {
		t.Fatalf("expected disabled_by_quota after retry, got %+v err %v", policy, err)
	}
	logs, _ = repository.ListAPIKeyEnforcementLogs(db, keyID, 10)
	if len(logs) == 0 || logs[0].Action != "disabled" || logs[0].Reason != "limit_breached" {
		t.Fatalf("expected disabled audit after retry, got %+v", logs)
	}
}

// TestRunnerReDisablesKeyStillPresentInCPA 覆盖崩溃窗口遗留态：状态已是 disabled_by_quota
// 但 key 仍在 CPA 服务，每轮评估必须幂等重发 DELETE 收敛，且成功时不再刷审计。
func TestRunnerReDisablesKeyStillPresentInCPA(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-zombie", "sk-buddy"}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	keyID := seedLimitedKey(t, db, "sk-zombie", 100, 150)
	// 直接落库模拟崩溃窗口：状态先写成功，DELETE 尚未发出。
	if err := repository.UpdateCPAAPIKeyPolicyRuntime(db, keyID,
		string(keypolicy.StateDisabledByQuota), keypolicy.WindowKey(keypolicy.DailyWindow(time.Now())), time.Now()); err != nil {
		t.Fatalf("seed disabled state: %v", err)
	}
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), nil)
	if err := runner.EvaluateOnce(context.Background()); err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(fake.keys) != 1 || fake.keys[0] != "sk-buddy" {
		t.Fatalf("expected lingering key removed from CPA, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, keyID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateDisabledByQuota) {
		t.Fatalf("expected state to stay disabled_by_quota, got %+v err %v", policy, err)
	}
	// 幂等收敛成功不产生任何新审计。
	logs, _ := repository.ListAPIKeyEnforcementLogs(db, keyID, 10)
	if len(logs) != 0 {
		t.Fatalf("expected no audit spam on idempotent re-disable, got %+v", logs)
	}
}

// TestRunnerRestoreWaitsForSharedKeyMutationLock 验证恢复的全量 PUT 与管理员写操作共用同一把锁：
// 管理端持锁期间 runner 不得发出 PUT，锁释放后才把 key 加回 CPA。
func TestRunnerRestoreWaitsForSharedKeyMutationLock(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-bystander"}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	keyID := seedLimitedKey(t, db, "sk-torn", 100, 10)
	if err := repository.UpdateCPAAPIKeyPolicyRuntime(db, keyID,
		string(keypolicy.StateDisabledByQuota), "2000-01-01", time.Now()); err != nil {
		t.Fatalf("seed disabled state: %v", err)
	}
	shared := &sync.Mutex{}
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), shared)

	// 模拟管理员 Create/Restore 正在进行（整个 GET→PUT 序列持锁）。
	shared.Lock()
	done := make(chan error, 1)
	go func() { done <- runner.EvaluateOnce(context.Background()) }()
	time.Sleep(200 * time.Millisecond)
	if len(fake.keys) != 1 || fake.keys[0] != "sk-bystander" {
		shared.Unlock()
		t.Fatalf("restore PUT must wait for the shared key mutation lock, got %v", fake.keys)
	}
	shared.Unlock()
	if err := <-done; err != nil {
		t.Fatalf("evaluate: %v", err)
	}
	if len(fake.keys) != 2 {
		t.Fatalf("expected key restored after lock release, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, keyID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateActive) {
		t.Fatalf("expected active state after restore, got %+v err %v", policy, err)
	}
}

// TestRunnerEvaluateOnceIsSingleFlight 验证并发调用 EvaluateOnce 不会交错收敛：
// 第一轮的 CPA GET 阻塞期间，第二轮必须等在 evalMu 上而不是同时打到 CPA。
func TestRunnerEvaluateOnceIsSingleFlight(t *testing.T) {
	db := newKeypolicyTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-sf", "sk-mate"}}
	server := startFakeCPA(t, fake)
	client := newCPAClient(t, server)
	seedLimitedKey(t, db, "sk-sf", 100, 10)
	runner := keypolicy.NewRunner(db, client, nil, time.Minute, logrus.New(), nil)

	gate := make(chan struct{})
	var mu sync.Mutex
	inFlight, maxInFlight := 0, 0
	fake.aroundGet = func(serve func()) {
		mu.Lock()
		inFlight++
		if inFlight > maxInFlight {
			maxInFlight = inFlight
		}
		mu.Unlock()
		serve()
		mu.Lock()
		inFlight--
		mu.Unlock()
		// gate 关闭前每个 GET 都停在写完响应之后、返回之前。
		<-gate
	}
	first := make(chan error, 1)
	second := make(chan error, 1)
	go func() { first <- runner.EvaluateOnce(context.Background()) }()
	time.Sleep(150 * time.Millisecond)
	go func() { second <- runner.EvaluateOnce(context.Background()) }()
	time.Sleep(150 * time.Millisecond)
	mu.Lock()
	observed := maxInFlight
	mu.Unlock()
	close(gate)
	if observed > 1 {
		t.Fatalf("expected single-flight evaluation, saw %d concurrent CPA gets", observed)
	}
	if err := <-first; err != nil {
		t.Fatalf("first evaluate: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second evaluate: %v", err)
	}
}
