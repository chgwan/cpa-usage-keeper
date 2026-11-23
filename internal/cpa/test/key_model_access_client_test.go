package cpa_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "cpa-usage-keeper/internal/cpa"
)

func TestDecodePluginsDirDecodesFiveHTMLEntitiesInOnePass(t *testing.T) {
	got := DecodePluginsDir(`  /opt/cpa/plugins/a&amp;b/&lt;test&gt;/&#34;q&#34;/&#39;s&#39;/&amp;lt;literal  `)
	want := `/opt/cpa/plugins/a&b/<test>/"q"/'s'/&lt;literal`
	if got != want {
		t.Fatalf("DecodePluginsDir = %q, want %q", got, want)
	}
}

func TestKeyModelAccessClientRoundTrip(t *testing.T) {
	var gotIfMatch string
	var gotPutBody KeyModelAccessPolicyDocument
	var gotInitDir string
	var gotPatchFile string

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer mgmt-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins/key-model-access/status":
			_, _ = w.Write([]byte(`{"plugin":"key-model-access","version":"0.2.2","revision":4,"policy_file":"plugins/key-model-access/config.toml","persistent_updates":true,"source":"plugins/key-model-access/config.toml","policy_count":1,"fail_closed":false}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins/key-model-access/models":
			_, _ = w.Write([]byte(`{"models":["claude-sonnet-4","gpt-5"]}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins/key-model-access/policies":
			w.Header().Set("ETag", `"rev-4"`)
			_, _ = w.Write([]byte(`{"policy":{"version":2,"policies":[{"caller_scope":"abc","allow_models":["gpt-*"],"deny_models":[]}]},"revision":4,"source":"config.toml"}`))
		case r.Method == http.MethodPut && r.URL.Path == "/v0/management/plugins/key-model-access/policies":
			gotIfMatch = r.Header.Get("If-Match")
			if err := json.NewDecoder(r.Body).Decode(&gotPutBody); err != nil {
				t.Errorf("decode put body: %v", err)
			}
			_, _ = w.Write([]byte(`{"ok":true,"policy_count":1,"persistent":true,"revision":5,"policy":{"version":2,"policies":[]}}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/plugins/key-model-access/reload":
			_, _ = w.Write([]byte(`{"ok":true,"policy_count":1,"revision":6}`))
		case r.Method == http.MethodGet && r.URL.Path == "/v0/management/plugins":
			_, _ = w.Write([]byte(`{"plugins_dir":"/data/plugins&amp;more"}`))
		case r.Method == http.MethodPost && r.URL.Path == "/v0/management/plugins/key-model-access/initialize-storage":
			var body struct {
				PluginsDir string `json:"plugins_dir"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotInitDir = body.PluginsDir
			w.WriteHeader(http.StatusCreated)
			_, _ = w.Write([]byte(`{"created":true,"policy_file":"/data/plugins&more/key-model-access/config.toml","policy_count":0}`))
		case r.Method == http.MethodPatch && r.URL.Path == "/v0/management/plugins/key-model-access/config":
			var body struct {
				PolicyFile string `json:"policy_file"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			gotPatchFile = body.PolicyFile
			_, _ = w.Write([]byte(`{"status":"ok"}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	client := NewClient(server.URL, "mgmt-secret", 2*time.Second, false)
	ctx := context.Background()

	status, code, err := client.FetchKeyModelAccessStatus(ctx)
	if err != nil || code != http.StatusOK || status.Version != "0.2.2" || !status.PersistentUpdates {
		t.Fatalf("FetchKeyModelAccessStatus = (%+v, %d, %v)", status, code, err)
	}

	models, code, err := client.FetchKeyModelAccessModels(ctx)
	if err != nil || code != http.StatusOK || len(models) != 2 {
		t.Fatalf("FetchKeyModelAccessModels = (%v, %d, %v)", models, code, err)
	}

	policies, etag, code, err := client.FetchKeyModelAccessPolicies(ctx)
	if err != nil || code != http.StatusOK || etag != `"rev-4"` || policies.Revision != 4 || len(policies.Policy.Policies) != 1 {
		t.Fatalf("FetchKeyModelAccessPolicies = (%+v, %q, %d, %v)", policies, etag, code, err)
	}

	replaced, code, err := client.ReplaceKeyModelAccessPolicies(ctx, KeyModelAccessPolicyDocument{
		Version: 2,
		Policies: []KeyModelAccessPolicyItem{
			{CallerScope: "abc", AllowModels: []string{"gpt-*"}},
		},
	}, etag)
	if err != nil || code != http.StatusOK || replaced.Revision != 5 {
		t.Fatalf("ReplaceKeyModelAccessPolicies = (%+v, %d, %v)", replaced, code, err)
	}
	if gotIfMatch != `"rev-4"` {
		t.Fatalf("expected If-Match %q, got %q", `"rev-4"`, gotIfMatch)
	}
	if gotPutBody.Policies[0].DenyModels == nil {
		t.Fatalf("expected nil DenyModels to serialize as empty array")
	}

	reloaded, code, err := client.ReloadKeyModelAccessPolicies(ctx)
	if err != nil || code != http.StatusOK || reloaded.Revision != 6 {
		t.Fatalf("ReloadKeyModelAccessPolicies = (%+v, %d, %v)", reloaded, code, err)
	}

	list, code, err := client.FetchManagementPluginsList(ctx)
	if err != nil || code != http.StatusOK || DecodePluginsDir(list.PluginsDir) != "/data/plugins&more" {
		t.Fatalf("FetchManagementPluginsList = (%+v, %d, %v)", list, code, err)
	}

	initRes, code, err := client.InitializeKeyModelAccessStorage(ctx, "/data/plugins&more")
	if err != nil || code != http.StatusCreated || !initRes.Created || gotInitDir != "/data/plugins&more" {
		t.Fatalf("InitializeKeyModelAccessStorage = (%+v, %d, %v)", initRes, code, err)
	}

	if code, err := client.PatchKeyModelAccessPluginConfig(ctx, initRes.PolicyFile); err != nil || code != http.StatusOK || gotPatchFile != initRes.PolicyFile {
		t.Fatalf("PatchKeyModelAccessPluginConfig = (%d, %v, got=%q)", code, err, gotPatchFile)
	}
}

func TestIsHTTPStatusDetectsUpstreamStatusCodes(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/v0/management/plugins/key-model-access/status" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.WriteHeader(http.StatusPreconditionFailed)
	}))
	defer server.Close()

	client := NewClient(server.URL, "mgmt", 2*time.Second, false)
	_, _, err := client.FetchKeyModelAccessStatus(context.Background())
	if !IsHTTPStatus(err, http.StatusNotFound) {
		t.Fatalf("expected IsHTTPStatus 404, got err=%v", err)
	}
	_, _, err = client.ReplaceKeyModelAccessPolicies(context.Background(), KeyModelAccessPolicyDocument{Version: 2}, `"rev-1"`)
	if !IsHTTPStatus(err, http.StatusPreconditionFailed) {
		t.Fatalf("expected IsHTTPStatus 412, got err=%v", err)
	}
}
