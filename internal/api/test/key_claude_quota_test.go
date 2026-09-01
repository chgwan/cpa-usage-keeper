package test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	. "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/auth"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/quota"
	"cpa-usage-keeper/internal/service"
)

type keyClaudeQuotaIdentityStub struct {
	calls int
}

func (s *keyClaudeQuotaIdentityStub) ListActiveClaudeAuthIndexes(context.Context) ([]string, error) {
	s.calls++
	return []string{"shared-claude-auth"}, nil
}

type keyClaudeQuotaProviderStub struct {
	QuotaProvider
	request quota.CacheRequest
}

func (s *keyClaudeQuotaProviderStub) GetCachedQuota(_ context.Context, request quota.CacheRequest) (quota.CacheResponse, error) {
	s.request = request
	percent := 35.0
	extraUsed := 25.0
	extraLimit := 100.0
	return quota.CacheResponse{Items: []quota.CachedQuotaItem{{
		AuthIndex: "shared-claude-auth", FileName: stringPointer("private.json"), Status: quota.RefreshTaskStatusCompleted,
		Quota: &quota.CheckResponse{ID: "shared-claude-auth", Quota: []quota.QuotaRow{
			{Key: "five_hour", Label: "5h", Scope: "private-scope", GroupDescription: "private group", UsedPercent: &percent},
			{Key: "extra_usage", Label: "Extra Usage", Used: &extraUsed, Limit: &extraLimit},
		}, Subscription: &quota.SubscriptionInfo{Provider: "private-provider", Plan: "Team", TierID: "private-tier-id", TierName: "private-tier-name"}},
		Error: "private error",
	}}}, nil
}

type sharedClaudeQuotaKeyProvider struct {
	rows map[int64]entities.CPAAPIKey
}

func (s *sharedClaudeQuotaKeyProvider) ListCPAAPIKeys(context.Context) ([]entities.CPAAPIKey, error) {
	rows := make([]entities.CPAAPIKey, 0, len(s.rows))
	for _, row := range s.rows {
		rows = append(rows, row)
	}
	return rows, nil
}

func (s *sharedClaudeQuotaKeyProvider) FindActiveCPAAPIKeyByValue(_ context.Context, value string) (entities.CPAAPIKey, error) {
	for _, row := range s.rows {
		if row.APIKey == value {
			return row, nil
		}
	}
	return entities.CPAAPIKey{}, service.ErrInvalidID
}

func (s *sharedClaudeQuotaKeyProvider) FindActiveCPAAPIKeyByID(_ context.Context, id int64) (entities.CPAAPIKey, error) {
	row, ok := s.rows[id]
	if !ok {
		return entities.CPAAPIKey{}, service.ErrInvalidID
	}
	return row, nil
}

func (s *sharedClaudeQuotaKeyProvider) UpdateCPAAPIKeyAlias(context.Context, int64, string) (entities.CPAAPIKey, error) {
	return entities.CPAAPIKey{}, service.ErrInvalidID
}

type sharedClaudeQuotaProviderStub struct {
	QuotaProvider
	requests []quota.CacheRequest
}

func (s *sharedClaudeQuotaProviderStub) GetCachedQuota(_ context.Context, request quota.CacheRequest) (quota.CacheResponse, error) {
	s.requests = append(s.requests, request)
	fiveHour := 41.0
	sevenDay := 63.0
	return quota.CacheResponse{Items: []quota.CachedQuotaItem{{
		AuthIndex: "shared-claude-auth",
		Status:    quota.RefreshTaskStatusCompleted,
		Quota: &quota.CheckResponse{Quota: []quota.QuotaRow{
			{Key: "five_hour", UsedPercent: &fiveHour},
			{Key: "seven_day", UsedPercent: &sevenDay},
		}},
	}}}, nil
}

func TestAPIKeyViewerClaudeQuotaUsesSharedAccountQuotaAndRedactsCredentialDetails(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	token, _, err := sessions.CreateAPIKeyViewerWithSource(42, auth.SessionSourceStandard)
	if err != nil {
		t.Fatalf("create API key viewer session: %v", err)
	}
	keyProvider := &keyViewerAnalysisKeyStub{row: entities.CPAAPIKey{
		ID: 42, APIKey: "sk-viewer123456", DisplayKey: "sk-*********123456", KeyAlias: "Viewer Key",
	}}
	identityProvider := &keyClaudeQuotaIdentityStub{}
	quotaProvider := &keyClaudeQuotaProviderStub{}
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	router := NewRouter(nil, nil, nil, nil, config, NewAuthHandler(config, sessions), "", OptionalProviders{
		CPAAPIKeys: keyProvider, KeyClaudeQuotaIdentities: identityProvider, Quota: quotaProvider,
	})

	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/key-claude-quotas?api_group_key=sk-other&auth_indexes=other-auth", nil)
	request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: token})
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("status=%d, want 200: %s", response.Code, response.Body.String())
	}
	if identityProvider.calls != 1 {
		t.Fatalf("expected one shared Claude identity lookup, got %d", identityProvider.calls)
	}
	if len(quotaProvider.request.AuthIndexes) != 1 || quotaProvider.request.AuthIndexes[0] != "shared-claude-auth" {
		t.Fatalf("expected cache lookup to use scoped auth indexes, got %+v", quotaProvider.request.AuthIndexes)
	}
	body := response.Body.String()
	for _, secret := range []string{
		"shared-claude-auth", "private.json", "private error", "auth_index", "file_name",
		"private-scope", "private group", "extra_usage", "Extra Usage", "private-provider", "private-tier-id", "private-tier-name",
	} {
		if strings.Contains(body, secret) {
			t.Fatalf("expected response to redact %q: %s", secret, body)
		}
	}
	if !strings.Contains(body, `"key":"five_hour"`) || !strings.Contains(body, `"usedPercent":35`) {
		t.Fatalf("expected response to retain Claude quota data: %s", body)
	}
	if !strings.Contains(body, `"subscription":{"plan":"Team"}`) {
		t.Fatalf("expected response to retain only the subscription plan: %s", body)
	}
}

func TestDifferentAPIKeyViewersReceiveIdenticalSharedClaudeAccountQuota(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	firstToken, _, err := sessions.CreateAPIKeyViewerWithSource(42, auth.SessionSourceStandard)
	if err != nil {
		t.Fatalf("create first API key viewer session: %v", err)
	}
	secondToken, _, err := sessions.CreateAPIKeyViewerWithSource(99, auth.SessionSourceStandard)
	if err != nil {
		t.Fatalf("create second API key viewer session: %v", err)
	}
	keyProvider := &sharedClaudeQuotaKeyProvider{rows: map[int64]entities.CPAAPIKey{
		42: {ID: 42, APIKey: "sk-first", DisplayKey: "sk-***first", KeyAlias: "First Viewer"},
		99: {ID: 99, APIKey: "sk-second", DisplayKey: "sk-***second", KeyAlias: "Second Viewer"},
	}}
	identityProvider := &keyClaudeQuotaIdentityStub{}
	quotaProvider := &sharedClaudeQuotaProviderStub{}
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	router := NewRouter(nil, nil, nil, nil, config, NewAuthHandler(config, sessions), "", OptionalProviders{
		CPAAPIKeys: keyProvider, KeyClaudeQuotaIdentities: identityProvider, Quota: quotaProvider,
	})

	requestQuota := func(token string) *httptest.ResponseRecorder {
		response := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodGet, "/api/v1/key-claude-quotas", nil)
		request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: token})
		router.ServeHTTP(response, request)
		return response
	}
	firstResponse := requestQuota(firstToken)
	secondResponse := requestQuota(secondToken)

	if firstResponse.Code != http.StatusOK || secondResponse.Code != http.StatusOK {
		t.Fatalf("expected both viewers to receive 200, got first=%d second=%d", firstResponse.Code, secondResponse.Code)
	}
	if firstResponse.Body.String() != secondResponse.Body.String() {
		t.Fatalf("expected identical shared Claude account quota, got first=%s second=%s", firstResponse.Body.String(), secondResponse.Body.String())
	}
	if identityProvider.calls != 2 || len(quotaProvider.requests) != 2 {
		t.Fatalf("expected each viewer to read the shared account cache, identities=%d cacheRequests=%d", identityProvider.calls, len(quotaProvider.requests))
	}
	for _, request := range quotaProvider.requests {
		if len(request.AuthIndexes) != 1 || request.AuthIndexes[0] != "shared-claude-auth" {
			t.Fatalf("expected shared Claude auth index for every viewer, got %+v", request.AuthIndexes)
		}
	}
}

func TestAPIKeyViewerCannotAccessQuotaAdministrationRoutes(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	token, _, err := sessions.CreateAPIKeyViewerWithSource(42, auth.SessionSourceStandard)
	if err != nil {
		t.Fatalf("create API key viewer session: %v", err)
	}
	keyProvider := &keyViewerAnalysisKeyStub{row: entities.CPAAPIKey{
		ID: 42, APIKey: "sk-viewer123456", DisplayKey: "sk-*********123456", KeyAlias: "Viewer Key",
	}}
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	router := NewRouter(nil, nil, nil, nil, config, NewAuthHandler(config, sessions), "", OptionalProviders{
		CPAAPIKeys: keyProvider, Quota: &keyClaudeQuotaProviderStub{},
	})

	tests := []struct {
		name   string
		method string
		path   string
		body   string
	}{
		{name: "inspection status", method: http.MethodGet, path: "/api/v1/quota/inspection"},
		{name: "start inspection", method: http.MethodPost, path: "/api/v1/quota/inspection"},
		{name: "refresh", method: http.MethodPost, path: "/api/v1/quota/refresh", body: `{"auth_indexes":["shared-claude-auth"]}`},
		{name: "refresh task", method: http.MethodGet, path: "/api/v1/quota/refresh/shared-claude-auth"},
		{name: "reset credits", method: http.MethodGet, path: "/api/v1/quota/reset-credits/shared-claude-auth"},
		{name: "reset", method: http.MethodPost, path: "/api/v1/quota/reset", body: `{"auth_index":"shared-claude-auth"}`},
		{name: "read auto refresh settings", method: http.MethodGet, path: "/api/v1/quota/auto-refresh/settings"},
		{name: "update auto refresh settings", method: http.MethodPut, path: "/api/v1/quota/auto-refresh/settings", body: `{"enabled":false}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			request := httptest.NewRequest(test.method, test.path, strings.NewReader(test.body))
			request.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
			request.Header.Set("Content-Type", "application/json")
			request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: token})
			router.ServeHTTP(response, request)

			if response.Code != http.StatusForbidden {
				t.Fatalf("status=%d, want 403: %s", response.Code, response.Body.String())
			}
		})
	}
}

func stringPointer(value string) *string {
	return &value
}
