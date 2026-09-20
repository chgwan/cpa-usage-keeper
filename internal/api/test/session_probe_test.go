package test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	keeperapi "cpa-usage-keeper/internal/api"
	keeperauth "cpa-usage-keeper/internal/auth"
)

// countingSessionStore 记录会话存储的读写次数，用来证明伪造 cookie 不产生数据库写入。
type countingSessionStore struct {
	mu           sync.Mutex
	sessions     map[string]keeperauth.Session
	getCalls     int
	deleteCalls  int
	deletedNames []string
}

func newCountingSessionStore() *countingSessionStore {
	return &countingSessionStore{sessions: make(map[string]keeperauth.Session)}
}

func (s *countingSessionStore) Save(token string, session keeperauth.Session) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[token] = session
	return nil
}

func (s *countingSessionStore) Get(token string) (keeperauth.Session, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.getCalls++
	session, ok := s.sessions[token]
	return session, ok, nil
}

func (s *countingSessionStore) List(time.Time) ([]keeperauth.SessionRecord, error) {
	return nil, nil
}

func (s *countingSessionStore) UpdateActivity(string, string, time.Time) error { return nil }

func (s *countingSessionStore) UpdateAdminAliasByTokenHash(string, string, time.Time) (int64, error) {
	return 0, nil
}

func (s *countingSessionStore) Delete(token string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.deleteCalls++
	s.deletedNames = append(s.deletedNames, token)
	delete(s.sessions, token)
	return nil
}

func (s *countingSessionStore) DeleteByTokenHash(string) (int64, error) { return 0, nil }

func (s *countingSessionStore) DeleteByRole(keeperauth.Role) (int64, error) { return 0, nil }

func (s *countingSessionStore) DeleteExpired(time.Time) error { return nil }

func (s *countingSessionStore) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.getCalls, s.deleteCalls
}

func newSessionProbeRouter(store keeperauth.SessionStore) (http.Handler, *keeperauth.SessionManager) {
	sessions := keeperauth.NewPersistentSessionManager(time.Hour, store)
	config := keeperapi.AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := keeperapi.NewAuthHandler(config, sessions)
	return keeperapi.NewRouter(nil, nil, nil, nil, config, handler, ""), sessions
}

func performSessionRequest(router http.Handler, token, remoteAddr string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/session", nil)
	request.RemoteAddr = remoteAddr
	if token != "" {
		request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: token})
	}
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)
	return response
}

func TestInventedSessionCookieDoesNotWriteToSessionStore(t *testing.T) {
	store := newCountingSessionStore()
	router, _ := newSessionProbeRouter(store)

	response := performSessionRequest(router, strings.Repeat("a", 64), "198.51.100.61:1234")

	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":false`) {
		t.Fatalf("unexpected response: %d %s", response.Code, response.Body.String())
	}
	getCalls, deleteCalls := store.counts()
	if deleteCalls != 0 {
		t.Fatalf("expected an unknown token to cause no delete, got %d deletes of %v", deleteCalls, store.deletedNames)
	}
	if getCalls != 1 {
		t.Fatalf("expected exactly one lookup for an unknown token, got %d", getCalls)
	}
}

func TestMalformedSessionCookieIsRejectedWithoutTouchingTheStore(t *testing.T) {
	store := newCountingSessionStore()
	router, _ := newSessionProbeRouter(store)

	for _, token := range []string{"not-a-token", strings.Repeat("a", 63), strings.Repeat("a", 65), strings.Repeat("Z", 64)} {
		response := performSessionRequest(router, token, "198.51.100.62:1234")
		if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":false`) {
			t.Fatalf("token %q: unexpected response %d %s", token, response.Code, response.Body.String())
		}
	}

	getCalls, deleteCalls := store.counts()
	if getCalls != 0 || deleteCalls != 0 {
		t.Fatalf("expected malformed tokens to skip the store entirely, got get=%d delete=%d", getCalls, deleteCalls)
	}
}

func TestLogoutWithInventedCookieDoesNotWriteToSessionStore(t *testing.T) {
	store := newCountingSessionStore()
	router, _ := newSessionProbeRouter(store)

	request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/logout", nil)
	request.RemoteAddr = "198.51.100.66:1234"
	request.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: strings.Repeat("d", 64)})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusNoContent {
		t.Fatalf("expected logout to return 204, got %d %s", response.Code, response.Body.String())
	}
	if _, deleteCalls := store.counts(); deleteCalls != 0 {
		t.Fatalf("expected logout with an unknown token to cause no delete, got %d", deleteCalls)
	}
}

func TestRepeatedUnknownSessionCookiesAreRateLimitedPerSource(t *testing.T) {
	store := newCountingSessionStore()
	router, _ := newSessionProbeRouter(store)
	remoteAddr := "198.51.100.63:1234"

	limited := false
	for index := 0; index < 60; index++ {
		response := performSessionRequest(router, strings.Repeat("b", 64), remoteAddr)
		if response.Code == http.StatusTooManyRequests {
			limited = true
			if response.Header().Get("Retry-After") == "" {
				t.Fatal("expected a rate-limited session probe to include Retry-After")
			}
			break
		}
		if response.Code != http.StatusOK {
			t.Fatalf("probe %d: unexpected response %d %s", index+1, response.Code, response.Body.String())
		}
	}
	if !limited {
		t.Fatal("expected repeated unknown session cookies to be rate limited")
	}

	getCallsBefore, _ := store.counts()
	performSessionRequest(router, strings.Repeat("b", 64), remoteAddr)
	getCallsAfter, _ := store.counts()
	if getCallsAfter != getCallsBefore {
		t.Fatalf("expected a limited source to stop querying the store, got %d extra lookups", getCallsAfter-getCallsBefore)
	}

	other := performSessionRequest(router, strings.Repeat("b", 64), "198.51.100.64:1234")
	if other.Code != http.StatusOK {
		t.Fatalf("expected a different source to keep its own budget, got %d", other.Code)
	}
}

func TestValidSessionSurvivesUnknownCookieProbesFromTheSameSource(t *testing.T) {
	store := newCountingSessionStore()
	router, sessions := newSessionProbeRouter(store)
	remoteAddr := "198.51.100.65:1234"
	token, _, err := sessions.Create()
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	for index := 0; index < 200; index++ {
		if response := performSessionRequest(router, strings.Repeat("c", 64), remoteAddr); response.Code == http.StatusTooManyRequests {
			break
		}
		if index == 199 {
			t.Fatal("expected the probe budget to be spent")
		}
	}

	response := performSessionRequest(router, token, remoteAddr)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"authenticated":true`) {
		t.Fatalf("expected a real session to stay usable, got %d %s", response.Code, response.Body.String())
	}
}
