package test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	. "cpa-usage-keeper/internal/api"
	"cpa-usage-keeper/internal/auth"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/service"
)

type keyViewerRankingKeyStub struct {
	row entities.CPAAPIKey
}

func (s *keyViewerRankingKeyStub) ListCPAAPIKeys(context.Context) ([]entities.CPAAPIKey, error) {
	return []entities.CPAAPIKey{s.row}, nil
}

func (s *keyViewerRankingKeyStub) FindActiveCPAAPIKeyByValue(context.Context, string) (entities.CPAAPIKey, error) {
	return s.row, nil
}

func (s *keyViewerRankingKeyStub) FindActiveCPAAPIKeyByID(_ context.Context, id int64) (entities.CPAAPIKey, error) {
	if id != s.row.ID {
		return entities.CPAAPIKey{}, service.ErrInvalidID
	}
	return s.row, nil
}

func (s *keyViewerRankingKeyStub) UpdateCPAAPIKeyAlias(context.Context, int64, string) (entities.CPAAPIKey, error) {
	return s.row, nil
}

func newKeyViewerRankingRouter(t *testing.T) (string, *rankingRouteProviderStub, *adminLocalRankingProviderStub, http.Handler) {
	t.Helper()
	sessions := auth.NewSessionManager(time.Hour)
	viewerToken, _, err := sessions.CreateAPIKeyViewer(42)
	if err != nil {
		t.Fatalf("create viewer session: %v", err)
	}
	community := &rankingRouteProviderStub{}
	local := &adminLocalRankingProviderStub{}
	keyProvider := &keyViewerRankingKeyStub{row: entities.CPAAPIKey{ID: 42, APIKey: "sk-viewer123456", KeyAlias: "Viewer"}}
	config := AuthConfig{
		Enabled:       true,
		LoginPassword: "secret",
		SessionTTL:    time.Hour,
	}
	router := NewRouter(nil, nil, nil, nil, config, NewAuthHandler(config, sessions), "", OptionalProviders{
		CPAAPIKeys:   keyProvider,
		Ranking:      community,
		LocalRanking: local,
	})
	return viewerToken, community, local, router
}

func viewerRankingRequest(method, target, token string) *http.Request {
	request := httptest.NewRequest(method, target, nil)
	request.AddCookie(&http.Cookie{Name: "cpa_usage_keeper_session", Value: token})
	return request
}

func TestKeyViewerRankingRoutesAreUnavailable(t *testing.T) {
	for _, tc := range []struct {
		method string
		path   string
		status int
	}{
		{http.MethodGet, "/key-ranking/leaderboards?period=today&metric=overall", http.StatusNotFound},
		{http.MethodGet, "/key-ranking/local/leaderboards?period=today&metric=overall", http.StatusNotFound},
		{http.MethodPost, "/key-ranking/join", http.StatusNotFound},
		{http.MethodPatch, "/key-ranking/local/profiles/42", http.StatusNotFound},
		{http.MethodGet, "/ranking/leaderboards?period=today&metric=overall", http.StatusForbidden},
		{http.MethodGet, "/ranking/local/leaderboards?period=today&metric=overall", http.StatusForbidden},
	} {
		t.Run(tc.method+" "+tc.path, func(t *testing.T) {
			viewerToken, community, local, router := newKeyViewerRankingRouter(t)
			response := httptest.NewRecorder()
			router.ServeHTTP(response, viewerRankingRequest(tc.method, "/api/v1"+tc.path, viewerToken))
			if response.Code != tc.status {
				t.Fatalf("expected %d, got %d %s", tc.status, response.Code, response.Body.String())
			}
			if community.leaderboardCalls != 0 || local.calls != 0 {
				t.Fatalf("viewer reached a ranking provider: community=%d local=%d", community.leaderboardCalls, local.calls)
			}
		})
	}
}

func TestKeyViewerSessionOmitsRankingCapability(t *testing.T) {
	viewerToken, _, _, router := newKeyViewerRankingRouter(t)
	response := httptest.NewRecorder()
	router.ServeHTTP(response, viewerRankingRequest(http.MethodGet, "/api/v1/auth/session", viewerToken))
	if response.Code != http.StatusOK {
		t.Fatalf("session unavailable: status=%d body=%s", response.Code, response.Body.String())
	}
	var session struct {
		Authenticated bool           `json:"authenticated"`
		Role          string         `json:"role"`
		APIKey        map[string]any `json:"api_key"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &session); err != nil {
		t.Fatalf("decode session: %v", err)
	}
	if !session.Authenticated || session.Role != "api_key_viewer" || session.APIKey["alias"] != "Viewer" {
		t.Fatalf("viewer session missing identity: %s", response.Body.String())
	}
	if _, ok := session.APIKey["local_ranking_enabled"]; ok {
		t.Fatalf("viewer session still advertises ranking access: %s", response.Body.String())
	}
}
