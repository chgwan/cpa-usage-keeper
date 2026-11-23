package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"cpa-usage-keeper/internal/service"

	"github.com/gin-gonic/gin"
)

type stubKeyModelAccessProvider struct {
	state      service.KeyModelAccessState
	getErr     error
	updateErr  error
	reloadErr  error
	gotIDs     []int64
	gotAllow   []string
	gotDeny    []string
	reloadCall int
}

func (s *stubKeyModelAccessProvider) GetState(context.Context) (service.KeyModelAccessState, error) {
	if s.getErr != nil {
		return service.KeyModelAccessState{}, s.getErr
	}
	return s.state, nil
}

func (s *stubKeyModelAccessProvider) UpdateRules(_ context.Context, ids []int64, allow, deny []string) (service.KeyModelAccessState, error) {
	s.gotIDs = ids
	s.gotAllow = allow
	s.gotDeny = deny
	if s.updateErr != nil {
		return service.KeyModelAccessState{}, s.updateErr
	}
	return s.state, nil
}

func (s *stubKeyModelAccessProvider) Reload(context.Context) (service.KeyModelAccessState, error) {
	s.reloadCall++
	if s.reloadErr != nil {
		return service.KeyModelAccessState{}, s.reloadErr
	}
	return s.state, nil
}

func newKeyModelAccessTestRouter(provider service.KeyModelAccessProvider) *gin.Engine {
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyModelAccessRoutes(group, provider)
	return router
}

func TestKeyModelAccessRoutesRoundTrip(t *testing.T) {
	provider := &stubKeyModelAccessProvider{
		state: service.KeyModelAccessState{
			Available: true,
			Status: &service.KeyModelAccessStatusView{
				Version:           "0.2.2",
				Revision:          3,
				PolicyFile:        "plugins/key-model-access/config.toml",
				PersistentUpdates: true,
				PolicyCount:       1,
			},
			Models: []string{"claude-sonnet-4", "gpt-5"},
			Policies: map[int64]service.KeyModelAccessRule{
				42: {AllowModels: []string{"gpt-*"}, DenyModels: []string{"*-preview"}},
			},
		},
	}
	router := newKeyModelAccessTestRouter(provider)

	getResp := httptest.NewRecorder()
	router.ServeHTTP(getResp, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys/model-access", nil))
	if getResp.Code != http.StatusOK {
		t.Fatalf("GET expected 200, got %d: %s", getResp.Code, getResp.Body.String())
	}
	if getResp.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("expected Cache-Control: no-store, got %q", getResp.Header().Get("Cache-Control"))
	}
	var decoded keyModelAccessStateResponse
	if err := json.Unmarshal(getResp.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode GET response: %v", err)
	}
	if !decoded.Available || decoded.Status == nil || decoded.Status.Revision != 3 || len(decoded.Policies["42"].AllowModels) != 1 {
		t.Fatalf("unexpected GET payload: %+v", decoded)
	}

	putBody := `{"apiKeyIds":["42","99"],"allowModels":["claude-*"],"denyModels":["*-mini"]}`
	putResp := httptest.NewRecorder()
	router.ServeHTTP(putResp, httptest.NewRequest(http.MethodPut, "/api/v1/usage/api-keys/model-access", bytes.NewReader([]byte(putBody))))
	if putResp.Code != http.StatusOK {
		t.Fatalf("PUT expected 200, got %d: %s", putResp.Code, putResp.Body.String())
	}
	if len(provider.gotIDs) != 2 || provider.gotIDs[0] != 42 || provider.gotIDs[1] != 99 {
		t.Fatalf("unexpected parsed IDs: %v", provider.gotIDs)
	}

	reloadResp := httptest.NewRecorder()
	router.ServeHTTP(reloadResp, httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys/model-access/reload", nil))
	if reloadResp.Code != http.StatusOK || provider.reloadCall != 1 {
		t.Fatalf("POST reload expected 200 & 1 call, got %d (%d)", reloadResp.Code, provider.reloadCall)
	}
}

func TestKeyModelAccessRoutesMapErrors(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
	}{
		{"unavailable", service.ErrKeyModelAccessUnavailable, http.StatusServiceUnavailable},
		{"conflict", service.ErrKeyModelAccessConflict, http.StatusConflict},
		{"not_persistent", service.ErrKeyModelAccessNotPersistent, http.StatusConflict},
		{"cpa_failed", service.ErrCPARequestFailed, http.StatusBadGateway},
		{"invalid_input", service.ErrInvalidInput, http.StatusBadRequest},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			router := newKeyModelAccessTestRouter(&stubKeyModelAccessProvider{updateErr: tc.err})
			resp := httptest.NewRecorder()
			body := `{"apiKeyIds":["1"],"allowModels":["gpt-*"],"denyModels":[]}`
			router.ServeHTTP(resp, httptest.NewRequest(http.MethodPut, "/api/v1/usage/api-keys/model-access", bytes.NewReader([]byte(body))))
			if resp.Code != tc.wantCode {
				t.Fatalf("expected status %d, got %d: %s", tc.wantCode, resp.Code, resp.Body.String())
			}
		})
	}
}

func TestKeyModelAccessGetReturnsUnavailableWhenProviderNil(t *testing.T) {
	router := newKeyModelAccessTestRouter(nil)
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys/model-access", nil))
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.Code)
	}
	var decoded keyModelAccessStateResponse
	_ = json.Unmarshal(resp.Body.Bytes(), &decoded)
	if decoded.Available {
		t.Fatalf("expected available=false when provider is nil")
	}
}
