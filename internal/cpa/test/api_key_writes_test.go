package cpa_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/cpa"
)

func newAPIKeyWriteClient(t *testing.T, handler http.HandlerFunc) (*cpa.Client, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return cpa.NewClient(server.URL, "mgmt-key", 5*time.Second, false), server
}

func TestReplaceManagementAPIKeysPutsFullList(t *testing.T) {
	var gotMethod, gotBody string
	client, _ := newAPIKeyWriteClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	result, err := client.ReplaceManagementAPIKeys(context.Background(), []string{"k1", "k2"})
	if err != nil {
		t.Fatalf("replace api keys: %v", err)
	}
	if gotMethod != http.MethodPut {
		t.Fatalf("expected PUT, got %s", gotMethod)
	}
	if !strings.Contains(gotBody, `"items"`) || !strings.Contains(gotBody, "k1") {
		t.Fatalf("expected items wrapper with keys, got %s", gotBody)
	}
	if result.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", result.StatusCode)
	}
	if result.Payload.Status != "ok" {
		t.Fatalf("expected status ok, got %q", result.Payload.Status)
	}
}

func TestUpdateManagementAPIKeyPatchesOldToNew(t *testing.T) {
	var gotBody string
	client, _ := newAPIKeyWriteClient(t, func(w http.ResponseWriter, r *http.Request) {
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	if _, err := client.UpdateManagementAPIKey(context.Background(), "old-key", "new-key"); err != nil {
		t.Fatalf("update api key: %v", err)
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(gotBody), &decoded); err != nil {
		t.Fatalf("decode request body %q: %v", gotBody, err)
	}
	if decoded["old"] != "old-key" || decoded["new"] != "new-key" {
		t.Fatalf("expected old/new body, got %s", gotBody)
	}
}

func TestDeleteManagementAPIKeyUsesValueQuery(t *testing.T) {
	var gotMethod, gotQuery string
	client, _ := newAPIKeyWriteClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotQuery = r.URL.RawQuery
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	if _, err := client.DeleteManagementAPIKey(context.Background(), "key to delete"); err != nil {
		t.Fatalf("delete api key: %v", err)
	}
	if gotMethod != http.MethodDelete {
		t.Fatalf("expected DELETE, got %s", gotMethod)
	}
	if gotQuery != "value=key+to+delete" {
		t.Fatalf("expected value query, got %q", gotQuery)
	}
}

func TestDeleteManagementAPIKeyRequiresNonEmptyKey(t *testing.T) {
	client, _ := newAPIKeyWriteClient(t, func(w http.ResponseWriter, _ *http.Request) {
		t.Fatal("handler must not be called for an empty key")
	})
	if _, err := client.DeleteManagementAPIKey(context.Background(), "  "); err == nil {
		t.Fatal("expected error for blank key")
	}
}
