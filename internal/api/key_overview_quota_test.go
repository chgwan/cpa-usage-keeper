package api

import (
	"context"
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

// quotaViewerKeyStub 只回答 viewer 会话需要的活跃 key 查询。
type quotaViewerKeyStub struct {
	row entities.CPAAPIKey
}

func (s *quotaViewerKeyStub) ListCPAAPIKeys(context.Context) ([]entities.CPAAPIKey, error) {
	return []entities.CPAAPIKey{s.row}, nil
}

func (s *quotaViewerKeyStub) FindActiveCPAAPIKeyByValue(context.Context, string) (entities.CPAAPIKey, error) {
	return s.row, nil
}

func (s *quotaViewerKeyStub) FindActiveCPAAPIKeyByID(context.Context, int64) (entities.CPAAPIKey, error) {
	return s.row, nil
}

func (s *quotaViewerKeyStub) UpdateCPAAPIKeyAlias(context.Context, int64, string) (entities.CPAAPIKey, error) {
	return s.row, nil
}

// keyOverviewQuotaTestRouter 组装带 viewer 会话与管理 provider 的路由。
func keyOverviewQuotaTestRouter(t *testing.T, management service.CPAAPIKeyManagementProvider) (http.Handler, string) {
	t.Helper()
	sessions := auth.NewSessionManager(time.Hour)
	token, _, err := sessions.CreateAPIKeyViewer(42)
	if err != nil {
		t.Fatalf("CreateAPIKeyViewer returned error: %v", err)
	}
	keyProvider := &quotaViewerKeyStub{row: entities.CPAAPIKey{ID: 42, DisplayKey: "sk-*********live"}}
	config := AuthConfig{Enabled: true, LoginPassword: "secret", SessionTTL: time.Hour}
	router := NewRouter(nil, nil, nil, nil, config, NewAuthHandler(config, sessions), "", OptionalProviders{
		CPAAPIKeys: keyProvider, CPAAPIKeyManagement: management,
	})
	return router, token
}

func TestKeyOverviewQuotaReturnsViewerCostWindows(t *testing.T) {
	limits := keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 20}}
	provider := &stubManagementProvider{policyView: service.CPAAPIKeyPolicyView{
		Policy: entities.CPAAPIKeyPolicy{Enabled: true, EnforcementState: "active"},
		Limits: limits,
		// 没有临时调整时 Effective 等于正常限额：keypolicy.ApplyAdjustments(limits, nil) 原样透传。
		Effective: keypolicy.ApplyAdjustments(limits, nil),
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

// TestKeyOverviewQuotaUsesEffectiveLimitNotNormalLimit 复现 viewer 端与实际执行脱节的问题：
// 一个正常限额 $100、本周期被临时调整 -$200 砍到 0（已经 disabled_by_quota）的 key，
// viewer 必须看到 limit=0 / ratio=1，而不是旧代码读 view.Limits 时展示的 "$42 / $100, 42%"。
func TestKeyOverviewQuotaUsesEffectiveLimitNotNormalLimit(t *testing.T) {
	limits := keypolicy.Limits{{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 100}}
	adjustment := -200.0
	provider := &stubManagementProvider{policyView: service.CPAAPIKeyPolicyView{
		Policy:    entities.CPAAPIKeyPolicy{Enabled: true, EnforcementState: "disabled_by_quota"},
		Limits:    limits,
		Effective: keypolicy.ApplyAdjustments(limits, keypolicy.Adjustments{keypolicy.LimitWindowWeekly: adjustment}),
		Usage: keypolicy.UsageByWindow{
			keypolicy.LimitWindowWeekly: {CostUSD: 42},
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
		Windows []struct {
			Window string   `json:"window"`
			Limit  *float64 `json:"limit"`
			Ratio  *float64 `json:"ratio"`
		} `json:"windows"`
	}
	if err := json.Unmarshal(resp.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode quota response: %v", err)
	}
	var weekly *struct {
		Window string   `json:"window"`
		Limit  *float64 `json:"limit"`
		Ratio  *float64 `json:"ratio"`
	}
	for i := range body.Windows {
		if body.Windows[i].Window == "weekly" {
			weekly = &body.Windows[i]
		}
	}
	if weekly == nil || weekly.Limit == nil || weekly.Ratio == nil {
		t.Fatalf("expected weekly window with limit/ratio, got %+v", body.Windows)
	}
	if *weekly.Limit != 0 {
		t.Fatalf("expected effective limit 0 (100 - 200 floored), got %v", *weekly.Limit)
	}
	if *weekly.Ratio != 1 {
		t.Fatalf("expected ratio 1 when effective limit is 0, got %v", *weekly.Ratio)
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
