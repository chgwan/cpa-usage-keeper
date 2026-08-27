package test

import (
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	keeperapp "cpa-usage-keeper/internal/app"
)

// TestAppWiresAuthTOTPResetIntoSetupGuard 锁定 config 到 authConfig 的完整传递：
// 若 App 构造漏传 TOTPReset，409 setup 守卫会在生产 wiring 中悄悄失效。
func TestAppWiresAuthTOTPResetIntoSetupGuard(t *testing.T) {
	// 准备：启用登录保护并打开 AUTH_TOTP_RESET，走真实 NewWithConfig 与路由。
	cfg := databasePoolTestConfig(filepath.Join(t.TempDir(), "totp-reset-wiring.db"))
	cfg.AuthEnabled = true
	cfg.LoginPassword = "secret"
	cfg.AuthSessionTTL = time.Hour
	cfg.AuthTOTPReset = true
	application, err := keeperapp.NewWithConfig(cfg)
	if err != nil {
		t.Fatalf("NewWithConfig returned error: %v", err)
	}
	defer application.Close()

	// 执行：通过真实路由登录管理员，再请求 TOTP setup。
	loginResp := httptest.NewRecorder()
	loginReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"password":"secret"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	application.Router.ServeHTTP(loginResp, loginReq)
	if loginResp.Code != http.StatusNoContent {
		t.Fatalf("admin login failed: %d %s", loginResp.Code, loginResp.Body.String())
	}
	sessionCookies := loginResp.Result().Cookies()
	if len(sessionCookies) == 0 {
		t.Fatal("expected login to set a session cookie")
	}

	setupResp := httptest.NewRecorder()
	setupReq := httptest.NewRequest(http.MethodPost, "/api/v1/auth/totp/setup", nil)
	setupReq.Header.Set("Content-Type", "application/json")
	setupReq.Header.Set("Cookie", sessionCookies[0].Name+"="+sessionCookies[0].Value)
	setupReq.Header.Set("X-CPA-Usage-Keeper-Request", "fetch")
	application.Router.ServeHTTP(setupResp, setupReq)

	// 断言：409 与守卫文案共同证明 cfg.AuthTOTPReset 已传入 handler 配置。
	if setupResp.Code != http.StatusConflict {
		t.Fatalf("expected 409 while AUTH_TOTP_RESET is active, got %d %s", setupResp.Code, setupResp.Body.String())
	}
	if !strings.Contains(setupResp.Body.String(), "totp reset is active") {
		t.Fatalf("expected totp reset guard error, got %s", setupResp.Body.String())
	}
}
