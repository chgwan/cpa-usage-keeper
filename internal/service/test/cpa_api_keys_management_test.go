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
	return service.NewCPAAPIKeyManagementService(db, client, nil), fake, db
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
	if _, _, err := provider.CreateCPAAPIKey(context.Background(), "", "sk-existing"); err == nil {
		t.Fatal("expected duplicate custom key to be rejected")
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
	if err := provider.SaveCPAAPIKeyPolicy(context.Background(), created.ID, bad, true); err == nil {
		t.Fatal("expected invalid limits to be rejected")
	}
}

func TestCPAWriteFailureSurfacesAsSentinelError(t *testing.T) {
	// CPA 一直 500 时，create 必须返回 ErrCPARequestFailed 而不是裸网络错误。
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(server.Close)
	db := openManagementTestDB(t)
	provider := service.NewCPAAPIKeyManagementService(db, cpa.NewClient(server.URL, "mgmt", 5*time.Second, false), nil)
	if _, _, err := provider.CreateCPAAPIKey(context.Background(), "", ""); !errors.Is(err, service.ErrCPARequestFailed) {
		t.Fatalf("expected ErrCPARequestFailed, got %v", err)
	}
}
