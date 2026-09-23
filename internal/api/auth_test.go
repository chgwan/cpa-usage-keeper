package api

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"cpa-usage-keeper/internal/auth"
)

// 本文件只保留 fork 独有的管理员 TOTP 登录测试；其余登录测试已随上游迁到 internal/api/test。

type fakeTOTPProvider struct {
	enrolled    bool
	enrolledErr error
	pending     bool
	verifyOK    bool
	confirmOK   bool
	disabled    bool
}

func (f *fakeTOTPProvider) Enrolled(context.Context) (bool, error) {
	if f.enrolledErr != nil {
		return false, f.enrolledErr
	}
	return f.enrolled, nil
}

func (f *fakeTOTPProvider) HasPending(context.Context) bool { return f.pending }

func (f *fakeTOTPProvider) CreatePending(context.Context) (string, string, error) {
	f.pending = true
	return "otpauth://totp/CPA%20Usage%20Keeper:admin?secret=JBSWY3DPEHPK3PXP", "JBSWY3DPEHPK3PXP", nil
}

func (f *fakeTOTPProvider) ConfirmPending(_ context.Context, code string) (bool, error) {
	return f.confirmOK && code == "123456", nil
}

func (f *fakeTOTPProvider) Verify(_ context.Context, _ string) (bool, error) {
	return f.verifyOK, nil
}

func (f *fakeTOTPProvider) Disable(context.Context) error {
	f.disabled = true
	f.enrolled = false
	f.pending = false
	return nil
}

func TestAuthLoginRequiresTOTPCodeWhenEnrolled(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := NewAuthHandler(config, sessions)
	totp := &fakeTOTPProvider{enrolled: true}
	handler.SetTOTPProvider(totp)
	router := NewRouter(nil, nil, nil, nil, config, handler, "")

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"secret"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusUnauthorized || !strings.Contains(resp.Body.String(), totpCodeRequiredError) {
		t.Fatalf("unexpected response: %d %s", resp.Code, resp.Body.String())
	}
}

func TestAuthLoginAcceptsPasswordWithValidTOTPCode(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := NewAuthHandler(config, sessions)
	handler.SetTOTPProvider(&fakeTOTPProvider{enrolled: true, verifyOK: true})
	router := NewRouter(nil, nil, nil, nil, config, handler, "")

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"secret","totp_code":"123456"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusNoContent {
		t.Fatalf("expected login status 204, got %d %s", resp.Code, resp.Body.String())
	}
	if len(resp.Result().Cookies()) == 0 {
		t.Fatal("expected auth cookie to be set")
	}
}

func TestAuthLoginRejectsPasswordWhenEnrollmentStateIsUnreadable(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := NewAuthHandler(config, sessions)
	handler.SetTOTPProvider(&fakeTOTPProvider{enrolled: true, verifyOK: true, enrolledErr: errors.New("enrollment storage unavailable")})
	router := NewRouter(nil, nil, nil, nil, config, handler, "")

	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"secret","totp_code":"123456"}`))
	req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
	req.Header.Set("Content-Type", "application/json")
	router.ServeHTTP(resp, req)

	if resp.Code != http.StatusInternalServerError {
		t.Fatalf("expected login to fail closed with 500, got %d %s", resp.Code, resp.Body.String())
	}
	if len(resp.Result().Cookies()) != 0 {
		t.Fatal("expected no session cookie when the second factor cannot be evaluated")
	}
}

func TestAuthLoginTOTPFailuresConsumeRateLimitBudget(t *testing.T) {
	sessions := auth.NewSessionManager(time.Hour)
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	handler := NewAuthHandler(config, sessions)
	// verifyOK flips to true later, proving a valid code is still blocked once the budget is spent.
	totp := &fakeTOTPProvider{enrolled: true, verifyOK: false}
	handler.SetTOTPProvider(totp)
	router := NewRouter(nil, nil, nil, nil, config, handler, "")

	doLogin := func() *httptest.ResponseRecorder {
		resp := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"secret","totp_code":"123456"}`))
		req.Header.Set(requestIntentHeaderName, requestIntentHeaderValueFetch)
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(resp, req)
		return resp
	}

	for i := 0; i < maxFailedLoginAttempts; i++ {
		resp := doLogin()
		if resp.Code != http.StatusUnauthorized || !strings.Contains(resp.Body.String(), invalidTOTPCodeError) {
			t.Fatalf("attempt %d unexpected response: %d %s", i, resp.Code, resp.Body.String())
		}
	}
	totp.verifyOK = true
	resp := doLogin()
	if resp.Code != http.StatusTooManyRequests {
		t.Fatalf("expected 429 after exhausting attempts, got %d %s", resp.Code, resp.Body.String())
	}
}
