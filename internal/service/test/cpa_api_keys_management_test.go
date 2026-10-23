package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"

	"gorm.io/gorm"
)

// fakeCPAAPIKeys 模拟 CPA api-keys 管理接口的内存实现。
type fakeCPAAPIKeys struct {
	keys []string
	// onDelete 在 DELETE 已把 key 移出内存列表之后回调，用来在服务写入后续状态前插入竞态动作。
	onDelete func()
}

func (f *fakeCPAAPIKeys) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{"api-keys": f.keys})
		case http.MethodPut:
			var body struct {
				Items []string `json:"items"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Errorf("decode put body: %v", err)
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			// 空 items 的 PUT 会清空 CPA 全部 key，服务层永远不允许发出。
			if len(body.Items) == 0 {
				t.Errorf("must never PUT an empty key list")
			}
			f.keys = body.Items
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		case http.MethodPatch:
			var body struct {
				Old string `json:"old"`
				New string `json:"new"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			for i, key := range f.keys {
				if key == body.Old {
					f.keys[i] = body.New
					_, _ = w.Write([]byte(`{"status":"ok"}`))
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
		case http.MethodDelete:
			value := r.URL.Query().Get("value")
			for i, key := range f.keys {
				if key == value {
					f.keys = append(f.keys[:i], f.keys[i+1:]...)
					if f.onDelete != nil {
						f.onDelete()
					}
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

// openManagementTestDB 复用本包既有 openMetadataTestDatabase 的迁移数据库打开方式。
func openManagementTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	return openMetadataTestDatabase(t, "cpa_api_keys_management.db")
}

// newManagementTestEnv 组装被测服务、内存 CPA 与真实临时数据库。
func newManagementTestEnv(t *testing.T) (service.CPAAPIKeyManagementProvider, *fakeCPAAPIKeys, *gorm.DB) {
	t.Helper()
	db := openManagementTestDB(t)
	fake := &fakeCPAAPIKeys{keys: []string{"sk-existing"}}
	server := httptest.NewServer(fake.handler(t))
	t.Cleanup(server.Close)
	client := cpa.NewClient(server.URL, "mgmt", 5*time.Second, false)
	return service.NewCPAAPIKeyManagementService(db, client, nil, nil), fake, db
}

func TestCreateCPAAPIKeyAppendsToCPAAndLocal(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	row, fullKey, err := provider.CreateCPAAPIKey(context.Background(), "team-a", "")
	if err != nil {
		t.Fatalf("create key: %v", err)
	}
	if !strings.HasPrefix(fullKey, "sk-") || len(fullKey) < 20 {
		t.Fatalf("expected generated sk- key, got %q", fullKey)
	}
	if len(fake.keys) != 2 || fake.keys[1] != fullKey {
		t.Fatalf("expected key appended in CPA, got %v", fake.keys)
	}
	loaded, err := repository.FindActiveCPAAPIKeyByValue(db, fullKey)
	if err != nil || loaded.KeyAlias != "team-a" || loaded.ID != row.ID {
		t.Fatalf("expected local row with alias, got %+v err %v", loaded, err)
	}
}

func TestCreateCPAAPIKeyRejectsDuplicateCustomKey(t *testing.T) {
	provider, _, _ := newManagementTestEnv(t)
	_, _, err := provider.CreateCPAAPIKey(context.Background(), "", "sk-existing")
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected duplicate custom key rejected as ErrInvalidInput, got %v", err)
	}
}

// TestCreateCPAAPIKeyRejectsInvalidCustomKeyWithSentinel 验证自定义 key 校验失败同样带 ErrInvalidInput。
func TestCreateCPAAPIKeyRejectsInvalidCustomKeyWithSentinel(t *testing.T) {
	provider, _, _ := newManagementTestEnv(t)
	_, _, err := provider.CreateCPAAPIKey(context.Background(), "", "has whitespace")
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid custom key rejected as ErrInvalidInput, got %v", err)
	}
}

func TestRegenerateCPAAPIKeyKeepsAliasAndPolicy(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, fullKey, _ := provider.CreateCPAAPIKey(context.Background(), "alias-1", "")
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 100},
	}); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	row, newKey, err := provider.RegenerateCPAAPIKey(context.Background(), created.ID)
	if err != nil {
		t.Fatalf("regenerate: %v", err)
	}
	if newKey == fullKey || !strings.Contains(strings.Join(fake.keys, ","), newKey) {
		t.Fatalf("expected CPA list to hold new key, got %v", fake.keys)
	}
	if row.KeyAlias != "alias-1" || row.ID != created.ID {
		t.Fatalf("expected alias and id preserved, got %+v", row)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil || policy.CPAAPIKeyID != created.ID {
		t.Fatalf("expected policy row still attached, got %+v err %v", policy, err)
	}
}

func TestRegenerateCPAAPIKeyRejectsQuotaDisabledKey(t *testing.T) {
	provider, _, db := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	// 先落一条策略，runner 只会改已有策略行的运行时字段。
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, nil); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	// 直接改库模拟 runner 置为 disabled_by_quota。
	if err := repository.UpdateCPAAPIKeyPolicyRuntime(db, created.ID, string(keypolicy.StateDisabledByQuota), "2026-08-31", time.Now()); err != nil {
		t.Fatalf("set state: %v", err)
	}
	if _, _, err := provider.RegenerateCPAAPIKey(context.Background(), created.ID); !errors.Is(err, service.ErrKeyDisabled) {
		t.Fatalf("expected regenerate on disabled key to be rejected, got %v", err)
	}
}

func TestDeleteCPAAPIKeyRemovesEverywhere(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, fullKey, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, nil); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	if err := provider.DeleteCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("delete key: %v", err)
	}
	if len(fake.keys) != 1 || fake.keys[0] == fullKey {
		t.Fatalf("expected key removed from CPA, got %v", fake.keys)
	}
	if _, err := repository.FindActiveCPAAPIKeyByID(db, created.ID); err == nil {
		t.Fatal("expected local row marked deleted")
	}
	if _, err := repository.FindCPAAPIKeyPolicy(db, created.ID); err == nil {
		t.Fatal("expected policy row removed")
	}
}

func TestDisableAndRestoreRoundTrip(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, fullKey, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	if err := provider.DisableCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if len(fake.keys) != 1 {
		t.Fatalf("expected key removed from CPA, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateDisabledManual) || !policy.AdminDisabled {
		t.Fatalf("expected manual-disabled policy, got %+v err %v", policy, err)
	}
	if err := provider.RestoreCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(fake.keys) != 2 || fake.keys[1] != fullKey {
		t.Fatalf("expected key re-added to CPA, got %v", fake.keys)
	}
}

func TestSaveCPAAPIKeyPolicyValidatesLimits(t *testing.T) {
	provider, _, _ := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	bad := keypolicy.Limits{{Type: "bogus", Window: keypolicy.LimitWindowDaily, Value: 1}}
	err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, bad)
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid limits rejected as ErrInvalidInput, got %v", err)
	}
}

// injectRunnerDisableOnPolicyRead 在服务读取策略行之后、写回之前注入一次 runner 式禁用：
// runner 的禁用路径不持有 keyMutations，这正是线上“读-改-写整行回写覆盖并发禁用”的交错点。
// GORM 的 After("gorm:query") 钩子在 rows 关闭后执行，此时可安全复用同一连接池写入。
func injectRunnerDisableOnPolicyRead(t *testing.T, db *gorm.DB, cpaAPIKeyID int64) {
	t.Helper()
	var fired bool
	err := db.Callback().Query().After("gorm:query").Register("test:runner_disable_race", func(tx *gorm.DB) {
		if fired {
			return
		}
		if _, ok := tx.Statement.Dest.(*entities.CPAAPIKeyPolicy); !ok {
			return
		}
		fired = true
		if err := db.Model(&entities.CPAAPIKeyPolicy{}).Where("cpa_api_key_id = ?", cpaAPIKeyID).
			Updates(map[string]any{
				"enforcement_state":   string(keypolicy.StateDisabledByQuota),
				"disabled_window_key": "2026-08-31",
				"last_evaluated_at":   time.Now(),
				"updated_at":          time.Now(),
			}).Error; err != nil {
			t.Errorf("inject runner disable: %v", err)
		}
	})
	if err != nil {
		t.Fatalf("register race hook: %v", err)
	}
}

// TestSaveCPAAPIKeyPolicyRejectsLegacyDimensions 验证已下线的 tokens / requests 维度
// 在保存入口就被 ErrInvalidInput 拒绝，不会重新写入策略表。
func TestSaveCPAAPIKeyPolicyRejectsLegacyDimensions(t *testing.T) {
	provider, _, _ := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	for _, legacy := range []string{"tokens", "requests"} {
		limits := keypolicy.Limits{{Type: keypolicy.LimitType(legacy), Window: keypolicy.LimitWindowDaily, Value: 100}}
		if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, limits); !errors.Is(err, service.ErrInvalidInput) {
			t.Fatalf("expected %s limit rejected as ErrInvalidInput, got %v", legacy, err)
		}
	}
}

// TestSaveCPAAPIKeyPolicyKeepsRunnerOwnedColumns 验证保存策略只写配置列：
// 与 runner 并发禁用交错时，enforcement_state/admin_disabled/last_evaluated_at 不能被读到的旧值复活。
func TestSaveCPAAPIKeyPolicyKeepsRunnerOwnedColumns(t *testing.T) {
	provider, _, db := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, nil); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	injectRunnerDisableOnPolicyRead(t, db, created.ID)
	newLimits := keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 42}}
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, newLimits); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if policy.EnforcementState != string(keypolicy.StateDisabledByQuota) {
		t.Fatalf("runner disable must survive concurrent policy save, got state %q", policy.EnforcementState)
	}
	if policy.LastEvaluatedAt == nil {
		t.Fatal("runner-written last_evaluated_at must not be clobbered by policy save")
	}
	if policy.AdminDisabled {
		t.Fatalf("runner disable does not set admin flag, got %+v", policy)
	}
	// enabled 已不再是可关闭的开关：保存入口永远强制写 true。
	if !policy.Enabled || !strings.Contains(policy.Limits, "42") {
		t.Fatalf("config columns must still update, got %+v", policy)
	}
}

// TestSaveCPAAPIKeyPolicyAlwaysForcesEnabled 验证限额开关已不可从应用层关闭：
// 即便策略行是迁移前残留的 enabled = 0，保存限额也会把它纠正为 enabled = 1。
func TestSaveCPAAPIKeyPolicyAlwaysForcesEnabled(t *testing.T) {
	provider, _, db := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	// 绕开已经去掉 enabled 参数的保存入口，直接落库模拟遗留的 enabled = 0 行。
	legacy := entities.CPAAPIKeyPolicy{CPAAPIKeyID: created.ID, Limits: "[]", Enabled: false, EnforcementState: string(keypolicy.StateActive)}
	if err := repository.UpsertCPAAPIKeyPolicy(db, &legacy); err != nil {
		t.Fatalf("seed legacy disabled policy: %v", err)
	}
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 10},
	}); err != nil {
		t.Fatalf("save policy: %v", err)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if !policy.Enabled {
		t.Fatalf("expected save to force enabled = 1, got %+v", policy)
	}
}

// TestDisableCPAAPIKeySurvivesSyncBetweenDeleteAndState 复现手动禁用的 TOCTOU 窗口：
// CPA DELETE 已生效、disabled_manual 尚未落库时插入一个 metadata sync tick，
// 本地行不能被软删（否则管理员看到的不是“手动禁用”而是 key 凭空消失）。
func TestDisableCPAAPIKeySurvivesSyncBetweenDeleteAndState(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	fake.onDelete = func() {
		// 此时 key 已被移出 CPA 列表，而服务还没写禁用状态：这正是 reap 窗口。
		if err := repository.SyncCPAAPIKeys(db, fake.keys, time.Now()); err != nil {
			t.Errorf("simulate metadata sync: %v", err)
		}
	}
	if err := provider.DisableCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := repository.FindActiveCPAAPIKeyByID(db, created.ID); err != nil {
		t.Fatalf("sync between cpa delete and state write must not reap the key row: %v", err)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateDisabledManual) || !policy.AdminDisabled {
		t.Fatalf("expected manual-disabled policy, got %+v err %v", policy, err)
	}
}

func TestCPAWriteFailureSurfacesAsSentinelError(t *testing.T) {
	// CPA 一直 500 时，create 必须返回 ErrCPARequestFailed 而不是裸网络错误。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	db := openManagementTestDB(t)
	provider := service.NewCPAAPIKeyManagementService(db, cpa.NewClient(server.URL, "mgmt", 5*time.Second, false), nil, nil)
	if _, _, err := provider.CreateCPAAPIKey(context.Background(), "", ""); !errors.Is(err, service.ErrCPARequestFailed) {
		t.Fatalf("expected ErrCPARequestFailed, got %v", err)
	}
}

// simulateMetadataSync 用 CPA 当前列表跑一次与周期 metadata sync 相同的本地落地逻辑。
func simulateMetadataSync(t *testing.T, db *gorm.DB, fake *fakeCPAAPIKeys) {
	t.Helper()
	if err := repository.SyncCPAAPIKeys(db, fake.keys, time.Now()); err != nil {
		t.Fatalf("simulate metadata sync: %v", err)
	}
}

func TestDisabledKeySurvivesMetadataSyncAndRestores(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, fullKey, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	if err := provider.DisableCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// 模拟周期 metadata sync：CPA 列表已不含被禁 key，本地行不能被误标删除。
	simulateMetadataSync(t, db, fake)
	if _, err := repository.FindActiveCPAAPIKeyByID(db, created.ID); err != nil {
		t.Fatalf("sync must not soft-delete policy-held key: %v", err)
	}
	// sync 之后恢复必须仍然可用，且 key 回到 CPA。
	if err := provider.RestoreCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("restore after sync: %v", err)
	}
	if len(fake.keys) != 2 || fake.keys[1] != fullKey {
		t.Fatalf("expected key re-added to CPA, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateActive) || policy.AdminDisabled {
		t.Fatalf("expected active policy after restore, got %+v err %v", policy, err)
	}
}

func TestDisableCPAAPIKeyToleratesAlreadyAbsentKey(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, _, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	if err := provider.DisableCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("first disable: %v", err)
	}
	// 第二次禁用时 key 已不在 CPA，GET-first 语义下必须直接成功而不是 404/502。
	if err := provider.DisableCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("second disable: %v", err)
	}
	if len(fake.keys) != 1 {
		t.Fatalf("expected CPA list untouched, got %v", fake.keys)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, created.ID)
	if err != nil || policy.EnforcementState != string(keypolicy.StateDisabledManual) || !policy.AdminDisabled {
		t.Fatalf("expected manual-disabled policy, got %+v err %v", policy, err)
	}
}

func TestDeleteCPAAPIKeyAfterDisableMarksRowDeleted(t *testing.T) {
	provider, fake, db := newManagementTestEnv(t)
	created, fullKey, _ := provider.CreateCPAAPIKey(context.Background(), "", "")
	if err := provider.DisableCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}
	// 模拟 sync：禁用中的 key 被策略保护，本地行仍可找到。
	simulateMetadataSync(t, db, fake)
	if _, err := repository.FindActiveCPAAPIKeyByID(db, created.ID); err != nil {
		t.Fatalf("sync must not soft-delete policy-held key: %v", err)
	}
	// 删除必须先清策略行再 sync，否则被保护的 key 会留下永久的 active 幽灵行。
	if err := provider.DeleteCPAAPIKey(context.Background(), created.ID); err != nil {
		t.Fatalf("delete after disable: %v", err)
	}
	if _, err := repository.FindActiveCPAAPIKeyByID(db, created.ID); err == nil {
		t.Fatal("expected local row marked deleted after delete")
	}
	if _, err := repository.FindCPAAPIKeyPolicy(db, created.ID); err == nil {
		t.Fatal("expected policy row removed after delete")
	}
	if len(fake.keys) != 1 || fake.keys[0] == fullKey {
		t.Fatalf("expected only bystander key left in CPA, got %v", fake.keys)
	}
}

func TestResetCPAAPIKeyQuotasWritesScopedResetsLogsAndWakesRunner(t *testing.T) {
	db := openManagementTestDB(t)
	keys := []entities.CPAAPIKey{{APIKey: "sk-q1", DisplayKey: "sk-q1"}, {APIKey: "sk-q2", DisplayKey: "sk-q2"}}
	for index := range keys {
		if err := db.Create(&keys[index]).Error; err != nil {
			t.Fatalf("seed key: %v", err)
		}
	}
	wakes := 0
	provider := service.NewCPAAPIKeyManagementServiceWithWaker(db, nil, nil, nil, func() { wakes++ })

	if err := provider.ResetCPAAPIKeyQuotas(context.Background(), nil, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}); err != nil {
		t.Fatalf("global reset: %v", err)
	}
	if err := provider.ResetCPAAPIKeyQuotas(context.Background(), []int64{keys[1].ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowMonthly, keypolicy.LimitWindowDaily, keypolicy.LimitWindowDaily}); err != nil {
		t.Fatalf("per-key reset: %v", err)
	}
	var resets []entities.APIKeyQuotaReset
	if err := db.Order("cpa_api_key_id, window").Find(&resets).Error; err != nil {
		t.Fatalf("load resets: %v", err)
	}
	got := make([]string, 0, len(resets))
	for _, row := range resets {
		got = append(got, fmt.Sprintf("%d:%s", row.CPAAPIKeyID, row.Window))
	}
	if want := fmt.Sprintf("[0:weekly %d:daily %d:monthly]", keys[1].ID, keys[1].ID); fmt.Sprint(got) != want {
		t.Fatalf("expected resets %s, got %v", want, got)
	}
	var logs []entities.APIKeyEnforcementLog
	if err := db.Where("action = ?", "quota_reset").Order("id").Find(&logs).Error; err != nil {
		t.Fatalf("load logs: %v", err)
	}
	if len(logs) != 3 || logs[0].Detail != "weekly (all keys)" || logs[2].CPAAPIKeyID != keys[1].ID || logs[2].Detail != "daily,monthly" {
		t.Fatalf("unexpected reset audit logs: %+v", logs)
	}
	if wakes != 2 {
		t.Fatalf("expected runner woken after each reset, got %d", wakes)
	}
	if err := provider.ResetCPAAPIKeyQuotas(context.Background(), nil, nil); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty windows rejected, got %v", err)
	}
	if err := provider.ResetCPAAPIKeyQuotas(context.Background(), []int64{999}, []keypolicy.LimitWindow{keypolicy.LimitWindowDaily}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected unknown key rejected, got %v", err)
	}
}

func TestUpdateCPAAPIKeyPoliciesMergesOnlyRequestedWindows(t *testing.T) {
	db := openManagementTestDB(t)
	keys := []entities.CPAAPIKey{{APIKey: "sk-p1", DisplayKey: "sk-p1"}, {APIKey: "sk-p2", DisplayKey: "sk-p2"}}
	for index := range keys {
		if err := db.Create(&keys[index]).Error; err != nil {
			t.Fatalf("seed key: %v", err)
		}
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), keys[0].ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 20},
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowMonthly, Value: 80},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	daily := 3.5
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), []int64{keys[0].ID, keys[1].ID}, service.CPAAPIKeyPolicyChanges{
		Limits: map[keypolicy.LimitWindow]*float64{keypolicy.LimitWindowDaily: &daily, keypolicy.LimitWindowMonthly: nil},
	}); err != nil {
		t.Fatalf("bulk update: %v", err)
	}
	first, err := provider.GetCPAAPIKeyPolicy(context.Background(), keys[0].ID)
	if err != nil {
		t.Fatalf("load first policy: %v", err)
	}
	if fmt.Sprint(first.Limits) != "[{cost daily 3.5} {cost weekly 20}]" || !first.Policy.Enabled {
		t.Fatalf("expected daily added, weekly kept, monthly removed, enabled forced on; got %v enabled=%v", first.Limits, first.Policy.Enabled)
	}
	second, err := provider.GetCPAAPIKeyPolicy(context.Background(), keys[1].ID)
	if err != nil {
		t.Fatalf("load second policy: %v", err)
	}
	if fmt.Sprint(second.Limits) != "[{cost daily 3.5}]" || !second.Policy.Enabled {
		t.Fatalf("expected new policy with daily only, got %v enabled=%v", second.Limits, second.Policy.Enabled)
	}

	bad := -1.0
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), []int64{keys[0].ID}, service.CPAAPIKeyPolicyChanges{
		Limits: map[keypolicy.LimitWindow]*float64{keypolicy.LimitWindowDaily: &bad},
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected non-positive limit rejected, got %v", err)
	}
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), nil, service.CPAAPIKeyPolicyChanges{
		Limits: map[keypolicy.LimitWindow]*float64{keypolicy.LimitWindowDaily: &daily},
	}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty key selection rejected, got %v", err)
	}
}

// TestUpdateCPAAPIKeyPoliciesRejectsEmptyChanges 验证 enabled 开关移除后，没有限额变更的批量请求
// 没有别的事可做，必须被拒绝，而不是悄悄返回成功。
func TestUpdateCPAAPIKeyPoliciesRejectsEmptyChanges(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-empty-changes", DisplayKey: "sk-empty-changes"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), []int64{key.ID}, service.CPAAPIKeyPolicyChanges{}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty changes rejected, got %v", err)
	}
}

// TestUpdateCPAAPIKeyPoliciesAlwaysForcesEnabled 验证批量更新入口同样不会保留 enabled = 0 的遗留行。
func TestUpdateCPAAPIKeyPoliciesAlwaysForcesEnabled(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-force-enable", DisplayKey: "sk-force-enable"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	legacy := entities.CPAAPIKeyPolicy{CPAAPIKeyID: key.ID, Limits: "[]", Enabled: false, EnforcementState: string(keypolicy.StateActive)}
	if err := repository.UpsertCPAAPIKeyPolicy(db, &legacy); err != nil {
		t.Fatalf("seed legacy disabled policy: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	daily := 5.0
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), []int64{key.ID}, service.CPAAPIKeyPolicyChanges{
		Limits: map[keypolicy.LimitWindow]*float64{keypolicy.LimitWindowDaily: &daily},
	}); err != nil {
		t.Fatalf("bulk update: %v", err)
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, key.ID)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if !policy.Enabled {
		t.Fatalf("expected bulk update to force enabled = 1, got %+v", policy)
	}
}

// TestSetCPAAPIKeyQuotaAdjustmentsAppliesOnlyConfiguredWindowsAndWakesRunner 验证只对已有正常限额的
// 窗口写入调整（没有限额的窗口记为 skipped），再次设置是替换而不是累加，并在提交后唤醒 runner。
func TestSetCPAAPIKeyQuotaAdjustmentsAppliesOnlyConfiguredWindowsAndWakesRunner(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-adj-1", DisplayKey: "sk-adj-1"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	wakes := 0
	provider := service.NewCPAAPIKeyManagementServiceWithWaker(db, nil, nil, nil, func() { wakes++ })
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}

	result, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID},
		[]keypolicy.LimitWindow{keypolicy.LimitWindowDaily, keypolicy.LimitWindowWeekly}, 500)
	if err != nil {
		t.Fatalf("set adjustments: %v", err)
	}
	if len(result.Applied) != 1 || result.Applied[0] != (service.QuotaAdjustmentTarget{APIKeyID: key.ID, Window: keypolicy.LimitWindowWeekly}) {
		t.Fatalf("expected weekly applied, got %+v", result.Applied)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != (service.QuotaAdjustmentTarget{APIKeyID: key.ID, Window: keypolicy.LimitWindowDaily}) {
		t.Fatalf("expected daily skipped (no normal limit), got %+v", result.Skipped)
	}
	if wakes != 1 {
		t.Fatalf("expected runner woken after commit, got %d", wakes)
	}

	var rows []entities.APIKeyQuotaAdjustment
	if err := db.Where("cpa_api_key_id = ?", key.ID).Find(&rows).Error; err != nil {
		t.Fatalf("load adjustments: %v", err)
	}
	if len(rows) != 1 || rows[0].Window != "weekly" || rows[0].DeltaUSD != 500 {
		t.Fatalf("expected one weekly adjustment row of 500, got %+v", rows)
	}

	// 再次设置是替换而不是累加。
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID},
		[]keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, -200); err != nil {
		t.Fatalf("replace adjustment: %v", err)
	}
	if err := db.Where("cpa_api_key_id = ?", key.ID).Find(&rows).Error; err != nil {
		t.Fatalf("reload adjustments: %v", err)
	}
	if len(rows) != 1 || rows[0].DeltaUSD != -200 {
		t.Fatalf("expected replaced delta -200, got %+v", rows)
	}

	logs, err := provider.ListCPAAPIKeyEnforcementLogs(context.Background(), key.ID, 10)
	if err != nil || len(logs) == 0 || logs[0].Action != "quota_adjusted" || logs[0].Reason != "admin_action" {
		t.Fatalf("expected quota_adjusted audit log, got %+v err %v", logs, err)
	}
}

// TestSetCPAAPIKeyQuotaAdjustmentsRejectsInvalidInput 覆盖裁决清单里的每一类非法输入：
// 空 ids、空 windows、零/NaN/±Inf 的 deltaUsd 全部映射为 ErrInvalidInput。
func TestSetCPAAPIKeyQuotaAdjustmentsRejectsInvalidInput(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-adj-invalid", DisplayKey: "sk-adj-invalid"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), nil, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, 10); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty ids rejected, got %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, nil, 10); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty windows rejected, got %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, 0); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected zero delta rejected, got %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, math.NaN()); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected NaN delta rejected, got %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, math.Inf(1)); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected +Inf delta rejected, got %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, math.Inf(-1)); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected -Inf delta rejected, got %v", err)
	}
}

// TestListCPAAPIKeyPolicySummariesReportsEffectiveLimitWithBaseAndAdjustment 验证列表摘要
// 的 tightest/limits 都报告叠加调整后的生效值，Base 仍是正常限额，Adjustment 是当前生效的 delta；
// 清除调整后摘要和数据库行都要回到没有调整的状态。
func TestListCPAAPIKeyPolicySummariesReportsEffectiveLimitWithBaseAndAdjustment(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-adj-summary", DisplayKey: "sk-adj-summary"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, 500); err != nil {
		t.Fatalf("set adjustment: %v", err)
	}

	summaries, err := provider.ListCPAAPIKeyPolicySummaries(context.Background())
	if err != nil {
		t.Fatalf("list summaries: %v", err)
	}
	summary, ok := summaries[key.ID]
	if !ok || summary.Tightest == nil {
		t.Fatalf("expected summary with tightest limit, got %+v ok=%v", summary, ok)
	}
	if summary.Tightest.Limit.Value != 600 || summary.Tightest.Base != 100 || summary.Tightest.Adjustment == nil || *summary.Tightest.Adjustment != 500 {
		t.Fatalf("expected effective 600 (base 100 + 500), got %+v", summary.Tightest)
	}
	if len(summary.Limits) != 1 || summary.Limits[0].Limit.Value != 600 || summary.Limits[0].Base != 100 {
		t.Fatalf("expected window usages to report effective value, got %+v", summary.Limits)
	}

	// GetCPAAPIKeyPolicy 的 Tightest 同样要反映生效值，但 Limits（可编辑配置）保持正常限额。
	view, err := provider.GetCPAAPIKeyPolicy(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	if view.Tightest == nil || view.Tightest.Limit.Value != 600 || view.Tightest.Base != 100 {
		t.Fatalf("expected single-key view tightest to use effective limit, got %+v", view.Tightest)
	}
	if len(view.Limits) != 1 || view.Limits[0].Value != 100 {
		t.Fatalf("expected single-key view limits to stay the normal stored config, got %+v", view.Limits)
	}

	if err := provider.ClearCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}); err != nil {
		t.Fatalf("clear adjustment: %v", err)
	}
	summaries, err = provider.ListCPAAPIKeyPolicySummaries(context.Background())
	if err != nil {
		t.Fatalf("list summaries after clear: %v", err)
	}
	summary = summaries[key.ID]
	if summary.Tightest == nil || summary.Tightest.Limit.Value != 100 || summary.Tightest.Adjustment != nil {
		t.Fatalf("expected adjustment cleared, got %+v", summary.Tightest)
	}
	var remaining int64
	if err := db.Model(&entities.APIKeyQuotaAdjustment{}).Where("cpa_api_key_id = ?", key.ID).Count(&remaining).Error; err != nil || remaining != 0 {
		t.Fatalf("expected adjustment row removed, count=%d err=%v", remaining, err)
	}
}

// TestClearCPAAPIKeyQuotaAdjustmentsWakesRunnerAndValidates 验证清除调整写审计、唤醒 runner，
// 并沿用与设置入口相同的 id/窗口校验（空 ids 400，未知 key 404）。
func TestClearCPAAPIKeyQuotaAdjustmentsWakesRunnerAndValidates(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-adj-clear", DisplayKey: "sk-adj-clear"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	wakes := 0
	provider := service.NewCPAAPIKeyManagementServiceWithWaker(db, nil, nil, nil, func() { wakes++ })
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}); err != nil {
		t.Fatalf("seed policy: %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, 500); err != nil {
		t.Fatalf("set adjustment: %v", err)
	}
	wakes = 0
	if err := provider.ClearCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}); err != nil {
		t.Fatalf("clear adjustment: %v", err)
	}
	if wakes != 1 {
		t.Fatalf("expected runner woken once after clear, got %d", wakes)
	}
	logs, err := provider.ListCPAAPIKeyEnforcementLogs(context.Background(), key.ID, 10)
	if err != nil || len(logs) == 0 || logs[0].Action != "quota_adjustment_cleared" {
		t.Fatalf("expected quota_adjustment_cleared audit log, got %+v err %v", logs, err)
	}

	if err := provider.ClearCPAAPIKeyQuotaAdjustments(context.Background(), nil, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected empty ids rejected, got %v", err)
	}
	if err := provider.ClearCPAAPIKeyQuotaAdjustments(context.Background(), []int64{999}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}); !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("expected unknown key rejected, got %v", err)
	}
}

// effectiveLimitForWindow 在测试里从 EffectiveLimits 里挑出某个窗口的那一条，没找到返回 nil。
func effectiveLimitForWindow(limits keypolicy.EffectiveLimits, window keypolicy.LimitWindow) *keypolicy.EffectiveLimit {
	for i := range limits {
		if limits[i].Window == window {
			return &limits[i]
		}
	}
	return nil
}

// TestSaveCPAAPIKeyPolicyClearsAdjustmentWhenWindowLimitRemoved 覆盖 item 3 的回归场景：
// weekly 限额 100 叠加 -100 的临时调整后，移除 weekly 限额再把它加回来（weekly 200），
// 如果旧的 adjustment 行没被一起清掉，它会在新周期内原样复活，effective 变成 100 而不是 200。
func TestSaveCPAAPIKeyPolicyClearsAdjustmentWhenWindowLimitRemoved(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-save-resurrect", DisplayKey: "sk-save-resurrect"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}); err != nil {
		t.Fatalf("seed weekly limit: %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, -100); err != nil {
		t.Fatalf("set adjustment: %v", err)
	}
	var adjustmentCount int64
	if err := db.Model(&entities.APIKeyQuotaAdjustment{}).Where("cpa_api_key_id = ?", key.ID).Count(&adjustmentCount).Error; err != nil || adjustmentCount != 1 {
		t.Fatalf("expected one adjustment row before removal, count=%d err=%v", adjustmentCount, err)
	}

	// 移除 weekly 限额：同一个 key 的 weekly adjustment 必须跟着被删掉。
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{}); err != nil {
		t.Fatalf("remove weekly limit: %v", err)
	}
	if err := db.Model(&entities.APIKeyQuotaAdjustment{}).Where("cpa_api_key_id = ?", key.ID).Count(&adjustmentCount).Error; err != nil || adjustmentCount != 0 {
		t.Fatalf("expected adjustment row deleted when limit removed, count=%d err=%v", adjustmentCount, err)
	}

	// 原周期内把 weekly 限额加回来（200）：旧 adjustment 已经被删了，不应该复活。
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 200},
	}); err != nil {
		t.Fatalf("re-add weekly limit: %v", err)
	}
	view, err := provider.GetCPAAPIKeyPolicy(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	weekly := effectiveLimitForWindow(view.Effective, keypolicy.LimitWindowWeekly)
	if weekly == nil || weekly.Value != 200 || weekly.Adjustment != nil {
		t.Fatalf("expected effective weekly 200 with no resurrected adjustment, got %+v", weekly)
	}
}

// TestUpdateCPAAPIKeyPoliciesClearsAdjustmentWhenWindowLimitRemoved 是 item 3 场景在批量更新
// 入口（UpdateCPAAPIKeyPolicies）下的等价回归：nil 值移除 weekly 限额时同样要清掉对应 adjustment。
func TestUpdateCPAAPIKeyPoliciesClearsAdjustmentWhenWindowLimitRemoved(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-bulk-resurrect", DisplayKey: "sk-bulk-resurrect"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}); err != nil {
		t.Fatalf("seed weekly limit: %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, -100); err != nil {
		t.Fatalf("set adjustment: %v", err)
	}

	// 用批量入口把 weekly 置为 nil（移除）。
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), []int64{key.ID}, service.CPAAPIKeyPolicyChanges{
		Limits: map[keypolicy.LimitWindow]*float64{keypolicy.LimitWindowWeekly: nil},
	}); err != nil {
		t.Fatalf("bulk remove weekly: %v", err)
	}
	var adjustmentCount int64
	if err := db.Model(&entities.APIKeyQuotaAdjustment{}).Where("cpa_api_key_id = ?", key.ID).Count(&adjustmentCount).Error; err != nil || adjustmentCount != 0 {
		t.Fatalf("expected adjustment row deleted when limit removed, count=%d err=%v", adjustmentCount, err)
	}

	// 再用批量入口把 weekly 加回来（200），旧 adjustment 不应该复活。
	weeklyValue := 200.0
	if err := provider.UpdateCPAAPIKeyPolicies(context.Background(), []int64{key.ID}, service.CPAAPIKeyPolicyChanges{
		Limits: map[keypolicy.LimitWindow]*float64{keypolicy.LimitWindowWeekly: &weeklyValue},
	}); err != nil {
		t.Fatalf("bulk re-add weekly: %v", err)
	}
	view, err := provider.GetCPAAPIKeyPolicy(context.Background(), key.ID)
	if err != nil {
		t.Fatalf("get policy: %v", err)
	}
	weekly := effectiveLimitForWindow(view.Effective, keypolicy.LimitWindowWeekly)
	if weekly == nil || weekly.Value != 200 || weekly.Adjustment != nil {
		t.Fatalf("expected effective weekly 200 with no resurrected adjustment, got %+v", weekly)
	}
}

// TestSetCPAAPIKeyQuotaAdjustmentsRejectsDeltaAboveMagnitudeCap 覆盖 item 4：|delta| 超过
// 1e9 的上限必须被拒绝为 ErrInvalidInput，避免 limit+delta 把响应 JSON 编码撑爆。
func TestSetCPAAPIKeyQuotaAdjustmentsRejectsDeltaAboveMagnitudeCap(t *testing.T) {
	db := openManagementTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-adj-cap", DisplayKey: "sk-adj-cap"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), key.ID, keypolicy.Limits{
		{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100},
	}); err != nil {
		t.Fatalf("seed weekly limit: %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, 1e9+1); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected delta above +1e9 rejected, got %v", err)
	}
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, -1e9-1); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected delta below -1e9 rejected, got %v", err)
	}
	// 恰好等于上限必须被接受（拒绝的是超过，不是达到）。
	if _, err := provider.SetCPAAPIKeyQuotaAdjustments(context.Background(), []int64{key.ID}, []keypolicy.LimitWindow{keypolicy.LimitWindowWeekly}, 1e9); err != nil {
		t.Fatalf("expected delta exactly at +1e9 accepted, got %v", err)
	}
	var adjustmentCount int64
	if err := db.Model(&entities.APIKeyQuotaAdjustment{}).Where("cpa_api_key_id = ?", key.ID).Count(&adjustmentCount).Error; err != nil || adjustmentCount != 1 {
		t.Fatalf("expected the accepted adjustment to persist, count=%d err=%v", adjustmentCount, err)
	}
}
