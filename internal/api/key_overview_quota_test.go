package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"cpa-usage-keeper/internal/auth"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/service"
)

// keyOverviewQuotaTestRouter 组装带 viewer 会话与管理 provider 的路由。
func keyOverviewQuotaTestRouter(t *testing.T, management service.CPAAPIKeyManagementProvider) (http.Handler, string) {
	t.Helper()
	sessions := auth.NewSessionManager(time.Hour)
	token, _, err := sessions.CreateAPIKeyViewer(42)
	if err != nil {
		t.Fatalf("CreateAPIKeyViewer returned error: %v", err)
	}
	keyProvider := &authCPAAPIKeyStub{row: entities.CPAAPIKey{ID: 42, DisplayKey: "sk-*********live"}}
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	router := NewRouter(nil, nil, nil, nil, config, NewAuthHandler(config, sessions), "", OptionalProviders{
		CPAAPIKeys: keyProvider, CPAAPIKeyManagement: management,
	})
	return router, token
}

func TestKeyOverviewQuotaReturnsViewerCostWindows(t *testing.T) {
	provider := &stubManagementProvider{policyView: service.CPAAPIKeyPolicyView{
		Policy: entities.CPAAPIKeyPolicy{Enabled: true, EnforcementState: "active"},
		Limits: keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 20}},
		Usage: keypolicy.UsageByWindow{
			keypolicy.LimitWindowDaily:   {CostUSD: 1.5},
			keypolicy.LimitWindowWeekly:  {CostUSD: 8.1},
			keypolicy.LimitWindowMonthly: {CostUSD: 23.4},
		},
	}}
	router, token := keyOverviewQuotaTestRouter(t, provider)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/key-overview/quota", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d %s", resp.Code, resp.Body.String())
	}
	var body struct {
		EnforcementState string `json:"enforcementState"`
		Windows          []struct {
			Window  string   `json:"window"`
			CostUSD float64  `json:"costUsd"`
			Limit   *float64 `json:"limit"`
			Ratio   *float64 `json:"ratio"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode quota response: %v", err)
	}
	if body.EnforcementState != "active" {
		t.Fatalf("expected active state, got %q", body.EnforcementState)
	}
	if len(body.Windows) != 3 || body.Windows[0].Window != "daily" || body.Windows[1].Window != "weekly" || body.Windows[2].Window != "monthly" {
		t.Fatalf("expected daily/weekly/monthly windows, got %+v", body.Windows)
	}
	if body.Windows[0].CostUSD != 1.5 || body.Windows[0].Limit != nil || body.Windows[0].Ratio != nil {
		t.Fatalf("daily window must expose spend without limit, got %+v", body.Windows[0])
	}
	weekly := body.Windows[1]
	if weekly.Limit == nil || weekly.Ratio == nil {
		t.Fatalf("weekly window must expose limit and ratio, got %+v", weekly)
	}
	// 期望值用运行时除法而非常量表达式：Go 的无类型常量按无限精度求值，与服务端 float64 除法有 1ulp 差异。
	wantRatio := weekly.CostUSD / *weekly.Limit
	if weekly.CostUSD != 8.1 || *weekly.Limit != 20 || *weekly.Ratio != wantRatio {
		t.Fatalf("weekly window values mismatch: cost=%v limit=%v ratio=%v", weekly.CostUSD, *weekly.Limit, *weekly.Ratio)
	}
	if body.Windows[2].CostUSD != 23.4 {
		t.Fatalf("monthly spend mismatch: %+v", body.Windows[2])
	}
}

func TestKeyOverviewQuotaWithoutManagementProviderReturns501(t *testing.T) {
	router, token := keyOverviewQuotaTestRouter(t, nil)
	resp := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/key-overview/quota", nil)
	req.AddCookie(&http.Cookie{Name: sessionCookieName, Value: token})
	router.ServeHTTP(resp, req)
	if resp.Code != http.StatusNotImplemented {
		t.Fatalf("expected 501, got %d %s", resp.Code, resp.Body.String())
	}
}

func TestKeyOverviewQuotaRequiresViewerSession(t *testing.T) {
	router, _ := keyOverviewQuotaTestRouter(t, &stubManagementProvider{})
	resp := httptest.NewRecorder()
	router.ServeHTTP(resp, httptest.NewRequest(http.MethodGet, "/api/v1/key-overview/quota", nil))
	if resp.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d %s", resp.Code, resp.Body.String())
	}
}
