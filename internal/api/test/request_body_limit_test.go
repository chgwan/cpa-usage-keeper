package test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	keeperapi "cpa-usage-keeper/internal/api"
	keeperauth "cpa-usage-keeper/internal/auth"
)

func TestAnonymousRoutesRejectOversizedRequestBodies(t *testing.T) {
	router := newLoginSecurityRouter(nil)
	for _, testCase := range []struct {
		name   string
		method string
		path   string
	}{
		{name: "logout", method: http.MethodPost, path: "/api/v1/auth/logout"},
		{name: "unknown api route", method: http.MethodPost, path: "/api/v1/does-not-exist"},
		{name: "health", method: http.MethodPost, path: "/healthz"},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			request := httptest.NewRequest(testCase.method, testCase.path, strings.NewReader("x"))
			request.ContentLength = (1 << 20) + 1
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
			request.RemoteAddr = "198.51.100.71:1234"
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != http.StatusRequestEntityTooLarge {
				t.Fatalf("expected 413, got %d body=%s", response.Code, response.Body.String())
			}
		})
	}
}

func TestOversizedStreamedBodyIsCutOffAtTheGeneralLimit(t *testing.T) {
	sessions := keeperauth.NewSessionManager(time.Hour)
	config := keeperapi.AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := keeperapi.NewAuthHandler(config, sessions)
	router := keeperapi.NewRouter(nil, nil, nil, nil, config, handler, "", keeperapi.OptionalProviders{AuthFiles: &authFilesManagementProvider{}})
	token, _, err := sessions.Create()
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/auth-files/status", strings.NewReader(`{"names":["`+strings.Repeat("x", 2<<20)+`"],"disabled":true}`))
	request.ContentLength = -1
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code == http.StatusOK {
		t.Fatal("expected a chunked body beyond the general limit to be rejected")
	}
}

func TestModeratelyLargeAuthenticatedBodyStillSucceeds(t *testing.T) {
	sessions := keeperauth.NewSessionManager(time.Hour)
	config := keeperapi.AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := keeperapi.NewAuthHandler(config, sessions)
	router := keeperapi.NewRouter(nil, nil, nil, nil, config, handler, "", keeperapi.OptionalProviders{AuthFiles: &authFilesManagementProvider{}})
	token, _, err := sessions.Create()
	if err != nil {
		t.Fatalf("create admin session: %v", err)
	}

	request := httptest.NewRequest(http.MethodPatch, "/api/v1/auth-files/status", strings.NewReader(`{"names":["`+strings.Repeat("x", 256<<10)+`"],"disabled":true}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	request.AddCookie(&http.Cookie{Name: standardSessionCookieName, Value: token})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, request)

	if response.Code != http.StatusOK {
		t.Fatalf("expected a 256 KiB authenticated body to be accepted, got %d body=%s", response.Code, response.Body.String())
	}
}
