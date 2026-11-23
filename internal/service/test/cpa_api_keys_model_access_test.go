package test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"cpa-usage-keeper/internal/cpa"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"
)

type fakeKeyModelAccessCPA struct {
	mu                sync.Mutex
	installed         bool
	keys              []string
	models            []string
	modelsErrStatus   int
	pluginsDir        string
	policyFile        string
	persistentUpdates bool
	failClosed        bool
	lastError         string
	revision          uint64
	policies          []cpa.KeyModelAccessPolicyItem
	initStorageErr    int
	patchConfigErr    int
	reloadPolicies    []cpa.KeyModelAccessPolicyItem

	initCalls  int
	patchCalls int
	putCalls   int
	failNextIf bool
}

func (f *fakeKeyModelAccessCPA) handler(t *testing.T) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v8/management/config/access/api-keys":
			_ = json.NewEncoder(w).Encode(append([]string{}, f.keys...))
			return
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/api-keys":
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
			return
		case r.Method == http.MethodDelete && r.URL.Path == "/v0/management/api-keys":
			val := r.URL.Query().Get("value")
			for i, key := range f.keys {
				if key == val {
					f.keys = append(f.keys[:i], f.keys[i+1:]...)
					_, _ = w.Write([]byte(`{"status":"ok"}`))
					return
				}
			}
			w.WriteHeader(http.StatusNotFound)
			return
		}

		if !f.installed && strings.HasPrefix(r.URL.Path, "/v0/management/plugins/key-model-access/") {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"error":"not_found"}`))
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins":
			_ = json.NewEncoder(w).Encode(map[string]string{"plugins_dir": f.pluginsDir})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins/key-model-access/status":
			source := "inline config"
			if f.policyFile != "" {
				source = f.policyFile
			}
			_ = json.NewEncoder(w).Encode(cpa.KeyModelAccessStatus{
				Plugin:            "key-model-access",
				Version:           "0.2.2",
				PolicyCount:       len(f.policies),
				Revision:          f.revision,
				PolicyFile:        f.policyFile,
				PersistentUpdates: f.persistentUpdates,
				Source:            source,
				UpdatedAt:         "2026-11-22T00:00:00Z",
				FailClosed:        f.failClosed,
				LastError:         f.lastError,
			})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins/key-model-access/models":
			if f.modelsErrStatus != 0 {
				w.WriteHeader(f.modelsErrStatus)
				_, _ = w.Write([]byte(`{"error":"unavailable"}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string][]string{"models": f.models})
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins/key-model-access/policies":
			w.Header().Set("ETag", fmt.Sprintf(`"rev-%d"`, f.revision))
			_ = json.NewEncoder(w).Encode(cpa.KeyModelAccessPoliciesResponse{
				Policy:   cpa.KeyModelAccessPolicyDocument{Version: 2, Policies: f.policies},
				Revision: f.revision,
				Source:   f.policyFile,
			})
		case r.Method == http.MethodPut && r.URL.Path == "/v0/management/plugins/key-model-access/policies":
			f.putCalls++
			if f.failNextIf {
				f.failNextIf = false
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(`{"error":"conflict"}`))
				return
			}
			wantETag := fmt.Sprintf(`"rev-%d"`, f.revision)
			if got := r.Header.Get("If-Match"); got != "" && got != wantETag {
				w.WriteHeader(http.StatusPreconditionFailed)
				_, _ = w.Write([]byte(`{"error":"conflict"}`))
				return
			}
			var doc cpa.KeyModelAccessPolicyDocument
			if err := json.NewDecoder(r.Body).Decode(&doc); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				return
			}
			f.policies = doc.Policies
			f.revision++
			_ = json.NewEncoder(w).Encode(cpa.KeyModelAccessReplacePoliciesResponse{
				OK:          true,
				PolicyCount: len(f.policies),
				Persistent:  f.persistentUpdates,
				Revision:    f.revision,
				Policy:      doc,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/plugins/key-model-access/reload":
			if !f.persistentUpdates {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"error":"policy_file is not configured"}`))
				return
			}
			if f.reloadPolicies != nil {
				f.policies = f.reloadPolicies
			}
			f.revision++
			_ = json.NewEncoder(w).Encode(cpa.KeyModelAccessReloadResponse{
				OK:          true,
				PolicyCount: len(f.policies),
				Revision:    f.revision,
			})
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/plugins/key-model-access/initialize-storage":
			f.initCalls++
			if f.initStorageErr != 0 {
				w.WriteHeader(f.initStorageErr)
				_, _ = w.Write([]byte(`{"error":"read-only"}`))
				return
			}
			var req struct {
				PluginsDir string `json:"plugins_dir"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			w.WriteHeader(http.StatusCreated)
			_ = json.NewEncoder(w).Encode(cpa.KeyModelAccessInitStorageResponse{
				Created:     true,
				PolicyFile:  req.PluginsDir + "/key-model-access/config.toml",
				PolicyCount: len(f.policies),
			})
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/plugins/key-model-access/config":
			f.patchCalls++
			if f.patchConfigErr != 0 {
				w.WriteHeader(f.patchConfigErr)
				_, _ = w.Write([]byte(`{"error":"readonly config"}`))
				return
			}
			var req struct {
				PolicyFile string `json:"policy_file"`
			}
			_ = json.NewDecoder(r.Body).Decode(&req)
			f.policyFile = req.PolicyFile
			f.persistentUpdates = true
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}
}

func TestKeyModelAccessServiceDetectsMissingPlugin(t *testing.T) {
	db := openMetadataTestDatabase(t, "kma_missing.db")
	fake := &fakeKeyModelAccessCPA{installed: false}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	svc := service.NewKeyModelAccessService(db, cpa.NewClient(server.URL, "mgmt", 2*time.Second, false))
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("expected nil error when plugin absent, got %v", err)
	}
	if state.Available {
		t.Fatalf("expected Available=false when plugin returns 404")
	}
}

func TestKeyModelAccessServiceAutoInitializesPersistenceAndMapsPolicies(t *testing.T) {
	db := openMetadataTestDatabase(t, "kma_init.db")
	now := time.Now()
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-alpha", "sk-beta"}, now); err != nil {
		t.Fatalf("seed keys: %v", err)
	}
	alpha, err := repository.FindActiveCPAAPIKeyByValue(db, "sk-alpha")
	if err != nil {
		t.Fatalf("find alpha: %v", err)
	}
	beta, err := repository.FindActiveCPAAPIKeyByValue(db, "sk-beta")
	if err != nil {
		t.Fatalf("find beta: %v", err)
	}

	fake := &fakeKeyModelAccessCPA{
		installed:         true,
		pluginsDir:        "/srv/cpa/plugins&amp;ext",
		persistentUpdates: false,
		models:            []string{"gpt-5", "claude-sonnet-4"},
		revision:          2,
		policies: []cpa.KeyModelAccessPolicyItem{
			{
				CallerScope: service.ComputeKeyModelAccessCallerScope("sk-alpha"),
				AllowModels: []string{"gpt-*"},
				DenyModels:  []string{"*-preview"},
			},
			// External/unknown scope must be preserved untouched during updates.
			{
				CallerScope: service.ComputeKeyModelAccessCallerScope("sk-external-disabled"),
				AllowModels: []string{"claude-*"},
				DenyModels:  []string{},
			},
		},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	svc := service.NewKeyModelAccessService(db, cpa.NewClient(server.URL, "mgmt", 2*time.Second, false))
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState: %v", err)
	}
	if !state.Available || state.Status == nil {
		t.Fatalf("expected Available=true with status, got %+v", state)
	}
	if !state.Status.PersistentUpdates || state.Status.PolicyFile != "/srv/cpa/plugins&ext/key-model-access/config.toml" {
		t.Fatalf("expected auto-initialized persistence, got %+v", state.Status)
	}
	if fake.initCalls != 1 || fake.patchCalls != 1 {
		t.Fatalf("expected 1 init & 1 patch call, got init=%d patch=%d", fake.initCalls, fake.patchCalls)
	}
	if !slices.Equal(state.Models, []string{"claude-sonnet-4", "gpt-5"}) {
		t.Fatalf("expected sorted models, got %v", state.Models)
	}
	if got := state.Policies[alpha.ID]; !slices.Equal(got.AllowModels, []string{"gpt-*"}) || !slices.Equal(got.DenyModels, []string{"*-preview"}) {
		t.Fatalf("unexpected alpha policy: %+v", got)
	}
	if _, exists := state.Policies[beta.ID]; exists {
		t.Fatalf("expected unconfigured beta key to have no policy entry")
	}

	// Batch update beta with normalized patterns, preserving alpha and external scope.
	updated, err := svc.UpdateRules(context.Background(), []int64{beta.ID}, []string{" claude-* ", "gpt-5", "claude-*"}, []string{"gpt-5-mini"})
	if err != nil {
		t.Fatalf("UpdateRules: %v", err)
	}
	if got := updated.Policies[beta.ID]; !slices.Equal(got.AllowModels, []string{"claude-*", "gpt-5"}) || !slices.Equal(got.DenyModels, []string{"gpt-5-mini"}) {
		t.Fatalf("unexpected beta policy after update: %+v", got)
	}
	if len(fake.policies) != 3 {
		t.Fatalf("expected alpha, beta, and preserved external policy (3 total), got %d: %+v", len(fake.policies), fake.policies)
	}

	// Clearing both allow & deny removes alpha's policy entry (back to default allow-all).
	cleared, err := svc.UpdateRules(context.Background(), []int64{alpha.ID}, []string{}, []string{})
	if err != nil {
		t.Fatalf("UpdateRules clear: %v", err)
	}
	if _, exists := cleared.Policies[alpha.ID]; exists {
		t.Fatalf("expected alpha policy to be removed when both lists are empty")
	}
	if len(fake.policies) != 2 {
		t.Fatalf("expected 2 policies remaining after clearing alpha, got %d", len(fake.policies))
	}
}

func TestKeyModelAccessServiceDegradesGracefullyWhenPersistenceSetupOrModelsFail(t *testing.T) {
	db := openMetadataTestDatabase(t, "kma_degrade.db")
	fake := &fakeKeyModelAccessCPA{
		installed:         true,
		pluginsDir:        "/readonly/plugins",
		persistentUpdates: false,
		initStorageErr:    http.StatusInternalServerError,
		modelsErrStatus:   http.StatusServiceUnavailable,
		revision:          1,
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	svc := service.NewKeyModelAccessService(db, cpa.NewClient(server.URL, "mgmt", 2*time.Second, false))
	state, err := svc.GetState(context.Background())
	if err != nil {
		t.Fatalf("expected graceful degradation, got err=%v", err)
	}
	if !state.Available || state.Status.PersistentUpdates {
		t.Fatalf("expected available memory-only state, got %+v", state)
	}
	if state.Status.PersistenceSetupError == "" {
		t.Fatalf("expected PersistenceSetupError to be populated")
	}
	if state.ModelsError == "" {
		t.Fatalf("expected ModelsError to be populated")
	}
	if _, err := svc.Reload(context.Background()); !errors.Is(err, service.ErrKeyModelAccessNotPersistent) {
		t.Fatalf("expected ErrKeyModelAccessNotPersistent on reload in memory mode, got %v", err)
	}
}

func TestKeyModelAccessServiceRejectsPluginIdentityAndConflict(t *testing.T) {
	db := openMetadataTestDatabase(t, "kma_errors.db")
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-native"}, time.Now()); err != nil {
		t.Fatalf("seed native: %v", err)
	}
	native, _ := repository.FindActiveCPAAPIKeyByValue(db, "sk-native")
	pluginRow := entities.CPAAPIKey{
		APIKey:         "plugin-key-1",
		DisplayKey:     "plugin-key-1",
		SourceType:     entities.CPAAPIKeySourcePlugin,
		AccessProvider: "plugin:custom",
	}
	if err := db.Create(&pluginRow).Error; err != nil {
		t.Fatalf("seed plugin row: %v", err)
	}

	fake := &fakeKeyModelAccessCPA{
		installed:         true,
		persistentUpdates: true,
		policyFile:        "config.toml",
		revision:          3,
		failNextIf:        true,
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	svc := service.NewKeyModelAccessService(db, cpa.NewClient(server.URL, "mgmt", 2*time.Second, false))
	if _, err := svc.UpdateRules(context.Background(), []int64{pluginRow.ID}, []string{"*"}, nil); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for plugin key, got %v", err)
	}
	if _, err := svc.UpdateRules(context.Background(), []int64{native.ID}, []string{"   "}, nil); !errors.Is(err, service.ErrInvalidInput) {
		t.Fatalf("expected ErrInvalidInput for blank pattern, got %v", err)
	}
	if _, err := svc.UpdateRules(context.Background(), []int64{native.ID}, []string{"gpt-*"}, nil); !errors.Is(err, service.ErrKeyModelAccessConflict) {
		t.Fatalf("expected ErrKeyModelAccessConflict on 412, got %v", err)
	}
}

func TestKeyModelAccessLifecycleMigratesOnRegenerateAndCleansUpOnDelete(t *testing.T) {
	db := openMetadataTestDatabase(t, "kma_lifecycle.db")
	if err := repository.SyncCPAAPIKeys(db, []string{"sk-one", "sk-two"}, time.Now()); err != nil {
		t.Fatalf("seed keys: %v", err)
	}
	one, _ := repository.FindActiveCPAAPIKeyByValue(db, "sk-one")
	two, _ := repository.FindActiveCPAAPIKeyByValue(db, "sk-two")

	fake := &fakeKeyModelAccessCPA{
		installed:         true,
		keys:              []string{"sk-one", "sk-two"},
		persistentUpdates: true,
		policyFile:        "config.toml",
		revision:          1,
		policies: []cpa.KeyModelAccessPolicyItem{
			{
				CallerScope: service.ComputeKeyModelAccessCallerScope("sk-one"),
				AllowModels: []string{"claude-*"},
				DenyModels:  []string{"claude-3-*"},
			},
			{
				CallerScope: service.ComputeKeyModelAccessCallerScope("sk-two"),
				AllowModels: []string{"gpt-*"},
				DenyModels:  []string{},
			},
		},
	}
	server := httptest.NewServer(fake.handler(t))
	defer server.Close()

	client := cpa.NewClient(server.URL, "mgmt", 2*time.Second, false)
	modelAccess := service.NewKeyModelAccessService(db, client)
	mgmt := service.NewCPAAPIKeyManagementServiceWithModelAccessHook(db, client, nil, nil, modelAccess)

	// Regenerating sk-one migrates its model access policy to the newly generated key.
	_, newKey, err := mgmt.RegenerateCPAAPIKey(context.Background(), one.ID)
	if err != nil {
		t.Fatalf("RegenerateCPAAPIKey: %v", err)
	}
	state, err := modelAccess.GetState(context.Background())
	if err != nil {
		t.Fatalf("GetState after regenerate: %v", err)
	}
	if rule := state.Policies[one.ID]; !slices.Equal(rule.AllowModels, []string{"claude-*"}) || !slices.Equal(rule.DenyModels, []string{"claude-3-*"}) {
		t.Fatalf("expected regenerated key to keep model access rule, got %+v", rule)
	}
	newScope := service.ComputeKeyModelAccessCallerScope(newKey)
	oldScope := service.ComputeKeyModelAccessCallerScope("sk-one")
	for _, item := range fake.policies {
		if item.CallerScope == oldScope {
			t.Fatalf("expected old caller_scope to be replaced")
		}
	}
	if !slices.ContainsFunc(fake.policies, func(item cpa.KeyModelAccessPolicyItem) bool { return item.CallerScope == newScope }) {
		t.Fatalf("expected new caller_scope %s in plugin policies", newScope)
	}

	// Deleting sk-two removes its model access policy while keeping sk-one's migrated policy.
	if err := mgmt.DeleteCPAAPIKey(context.Background(), two.ID); err != nil {
		t.Fatalf("DeleteCPAAPIKey: %v", err)
	}
	if len(fake.policies) != 1 || fake.policies[0].CallerScope != newScope {
		t.Fatalf("expected only regenerated key policy to remain after delete, got %+v", fake.policies)
	}
}
