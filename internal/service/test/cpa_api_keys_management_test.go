package test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"

	"gorm.io/gorm"
)

// fakeCPAAPIKeys 模拟 CPA api-keys 管理接口的内存实现。
type fakeCPAAPIKeys struct {
	keys []string
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
		{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 100},
	}, true); err != nil {
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
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, nil, true); err != nil {
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
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, nil, true); err != nil {
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
	err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, bad, true)
	if !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected invalid limits rejected as ErrInvalidInput, got %v", err)
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
