package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/auth"
	"github.com/gin-gonic/gin"
)

func newTOTPTestRouter(t *testing.T, config AuthConfig, totp *fakeTOTPProvider) (*gin.Engine, string) {
	t.Helper()
	sessions := auth.NewSessionManager(time.Hour)
	handler := NewAuthHandler(config, sessions)
	if totp != nil {
		handler.SetTOTPProvider(totp)
	}
	router := NewRouter(nil, nil, nil, nil, config, handler, "")

	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"secret"}`))
	loginReq.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	loginReq.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusNoContent {
		t.Fatalf("admin login failed: %d %s", loginResp.Code, loginResp.Body.String())
	}
	cookie := loginResp.Result().Cookies()[0]
	return router, cookie.Name + "=" + cookie.Value
}

func doTOTPRequest(router *gin.Engine, cookie string, method, path, body string) *httptest.ResponseRecorder {
	resp := httptest.NewRecorder()
	var req *http.Request
	if body == "" {
		req = httptest.NewRequest(method, path, nil)
	} else {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Cookie", cookie)
	if method != http.MethodGet {
		req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	}
	router.ServeHTTP(resp, req)
	return resp
}

func TestTOTPManagementRoutesRequireAdminSession(t *testing.T) {
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := NewAuthHandler(config, auth.NewSessionManager(time.Hour))
	router := NewRouter(nil, nil, nil, nil, config, handler, "")

	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/auth/totp", nil))
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 without session, got %d", resp.Code)
	}
}

func TestTOTPSetupBlockedWhileResetActive(t *testing.T) {
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour, TOTPReset: true}
	router, cookie := newTOTPTestRouter(t, config, &fakeTOTPProvider{})

	resp := doTOTPRequest(router, cookie, http.MethodPost, "/api/v1/auth/totp/setup", `{}`)
	if resp.Code != http.StatusConflict {
		t.Fatalf("expected 409 while reset active, got %d %s", resp.Code, resp.Body.String())
	}
}

func TestTOTPSetupConfirmDisableCycle(t *testing.T) {
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	totp := &fakeTOTPProvider{}
	router, cookie := newTOTPTestRouter(t, config, totp)

	resp := doTOTPRequest(router, cookie, http.MethodGet, "/api/v1/auth/totp", "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"enabled":false`) {
		t.Fatalf("unexpected status: %d %s", resp.Code, resp.Body.String())
	}

	resp = doTOTPRequest(router, cookie, http.MethodPost, "/api/v1/auth/totp/setup", `{}`)
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), "otpauth_uri") {
		t.Fatalf("unexpected setup response: %d %s", resp.Code, resp.Body.String())
	}

	resp = doTOTPRequest(router, cookie, http.MethodPost, "/api/v1/auth/totp/confirm", `{"code":"000000"}`)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong code, got %d %s", resp.Code, resp.Body.String())
	}

	totp.confirmOK = true
	resp = doTOTPRequest(router, cookie, http.MethodPost, "/api/v1/auth/totp/confirm", `{"code":"123456"}`)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 for confirmed code, got %d %s", resp.Code, resp.Body.String())
	}

	totp.enrolled = true
	totp.verifyOK = true
	resp = doTOTPRequest(router, cookie, http.MethodGet, "/api/v1/auth/totp", "")
	if resp.Code != http.StatusOK || !strings.Contains(resp.Body.String(), `"enabled":true`) {
		t.Fatalf("unexpected status: %d %s", resp.Code, resp.Body.String())
	}

	resp = doTOTPRequest(router, cookie, http.MethodPost, "/api/v1/auth/totp/disable", `{"password":"wrong","code":"123456"}`)
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for wrong password, got %d %s", resp.Code, resp.Body.String())
	}

	resp = doTOTPRequest(router, cookie, http.MethodPost, "/api/v1/auth/totp/disable", `{"password":"secret","code":"123456"}`)
	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected 204 after disable, got %d %s", resp.Code, resp.Body.String())
	}
	if !totp.disabled {
		t.Fatal("expected provider Disable to be called")
	}
}
