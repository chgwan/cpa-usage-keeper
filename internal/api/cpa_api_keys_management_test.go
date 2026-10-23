package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"cpa-usage-keeper/internal/config"
	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/repository"
	"cpa-usage-keeper/internal/service"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// stubManagementProvider 桩掉生命周期与策略服务，只回填测试关心的返回值。
type stubManagementProvider struct {
	createErr    error
	regenErr     error
	saveErr      error
	policyView   service.CPAAPIKeyPolicyView
	limitsIn     keypolicy.Limits
	summaries    map[int64]service.CPAAPIKeyPolicySummary
	summariesErr error
	err          error

	resetIDs      []int64
	resetWindows  []keypolicy.LimitWindow
	updateIDs     []int64
	updateChanges service.CPAAPIKeyPolicyChanges

	setAdjIDs     []int64
	setAdjWindows []keypolicy.LimitWindow
	setAdjDelta   float64
	setAdjResult  service.QuotaAdjustmentResult
	setAdjErr     error

	clearAdjIDs     []int64
	clearAdjWindows []keypolicy.LimitWindow
	clearAdjErr     error
}

func (s *stubManagementProvider) CreateCPAAPIKey(_ context.Context, alias, _ string) (entities.CPAAPIKey, string, error) {
	if s.createErr != nil {
		return entities.CPAAPIKey{}, "", s.createErr
	}
	return entities.CPAAPIKey{ID: 9, APIKey: "sk-new", KeyAlias: alias}, "sk-new-full", nil
}

func (s *stubManagementProvider) RegenerateCPAAPIKey(_ context.Context, id int64) (entities.CPAAPIKey, string, error) {
	if s.regenErr != nil {
		return entities.CPAAPIKey{}, "", s.regenErr
	}
	return entities.CPAAPIKey{ID: id, APIKey: "sk-regen"}, "sk-regen-full", nil
}

func (s *stubManagementProvider) DeleteCPAAPIKey(context.Context, int64) error  { return nil }
func (s *stubManagementProvider) DisableCPAAPIKey(context.Context, int64) error { return nil }
func (s *stubManagementProvider) RestoreCPAAPIKey(context.Context, int64) error { return nil }

func (s *stubManagementProvider) GetCPAAPIKeyPolicy(_ context.Context, _ int64) (service.CPAAPIKeyPolicyView, error) {
	return s.policyView, nil
}

// SaveCPAAPIKeyPolicy 复刻真实服务“先校验再保存”的契约：非法限额包装 ErrInvalidInput 走 400，
// saveErr 用于注入基础设施错误验证未知错误的 500 语义。
func (s *stubManagementProvider) SaveCPAAPIKeyPolicy(_ context.Context, _ int64, limits keypolicy.Limits) error {
	s.limitsIn = limits
	if s.saveErr != nil {
		return s.saveErr
	}
	if err := limits.Validate(); err != nil {
		return fmt.Errorf("%w: %v", service.ErrInvalidInput, err)
	}
	return nil
}

func (s *stubManagementProvider) ListCPAAPIKeyEnforcementLogs(_ context.Context, _ int64, _ int) ([]entities.APIKeyEnforcementLog, error) {
	return []entities.APIKeyEnforcementLog{{ID: 1, CPAAPIKeyID: 9, Action: "disabled", Reason: "limit_breached"}}, nil
}

func (s *stubManagementProvider) ResetCPAAPIKeyQuotas(_ context.Context, ids []int64, windows []keypolicy.LimitWindow) error {
	s.resetIDs, s.resetWindows = ids, windows
	return s.err
}

// UpdateCPAAPIKeyPolicies 复刻真实服务“没有限额要改就 400”的契约：enabled 开关移除后，
// 没有 limits 的批量请求已经无事可做。
func (s *stubManagementProvider) UpdateCPAAPIKeyPolicies(_ context.Context, ids []int64, changes service.CPAAPIKeyPolicyChanges) error {
	s.updateIDs, s.updateChanges = ids, changes
	if s.err != nil {
		return s.err
	}
	if len(changes.Limits) == 0 {
		return fmt.Errorf("%w: nothing to update", service.ErrInvalidInput)
	}
	return nil
}

func (s *stubManagementProvider) ListCPAAPIKeyPolicySummaries(_ context.Context) (map[int64]service.CPAAPIKeyPolicySummary, error) {
	if s.summariesErr != nil {
		return nil, s.summariesErr
	}
	return s.summaries, nil
}

func (s *stubManagementProvider) SetCPAAPIKeyQuotaAdjustments(_ context.Context, ids []int64, windows []keypolicy.LimitWindow, deltaUSD float64) (service.QuotaAdjustmentResult, error) {
	s.setAdjIDs, s.setAdjWindows, s.setAdjDelta = ids, windows, deltaUSD
	if s.setAdjErr != nil {
		return service.QuotaAdjustmentResult{}, s.setAdjErr
	}
	return s.setAdjResult, nil
}

func (s *stubManagementProvider) ClearCPAAPIKeyQuotaAdjustments(_ context.Context, ids []int64, windows []keypolicy.LimitWindow) error {
	s.clearAdjIDs, s.clearAdjWindows = ids, windows
	return s.clearAdjErr
}

// stubCPAAPIKeyListProvider 给列表路由提供最小 CPAAPIKeyProvider 实现。
type stubCPAAPIKeyListProvider struct {
	rows []entities.CPAAPIKey
}

func (s stubCPAAPIKeyListProvider) ListCPAAPIKeys(context.Context) ([]entities.CPAAPIKey, error) {
	return s.rows, nil
}

func (s stubCPAAPIKeyListProvider) FindActiveCPAAPIKeyByValue(context.Context, string) (entities.CPAAPIKey, error) {
	return entities.CPAAPIKey{}, gorm.ErrRecordNotFound
}

func (s stubCPAAPIKeyListProvider) FindActiveCPAAPIKeyByID(context.Context, int64) (entities.CPAAPIKey, error) {
	return entities.CPAAPIKey{}, gorm.ErrRecordNotFound
}

func (s stubCPAAPIKeyListProvider) UpdateCPAAPIKeyAlias(context.Context, int64, string) (entities.CPAAPIKey, error) {
	return entities.CPAAPIKey{}, nil
}

func newManagementTestRouter(provider service.CPAAPIKeyManagementProvider) *gin.Engine {
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyManagementRoutes(group, provider)
	return router
}

func TestCreateRouteReturnsFullKeyOnce(t *testing.T) {
	router := newManagementTestRouter(&stubManagementProvider{})
	response := httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"keyAlias": "a", "key": ""})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys", bytes.NewReader(body))
	router.ServeHTTP(response, request)
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var decoded struct {
		ID  string `json:"id"`
		Key string `json:"key"`
	}
	_ = json.Unmarshal(response.Body.Bytes(), &decoded)
	if decoded.ID != "9" || decoded.Key != "sk-new-full" {
		t.Fatalf("expected one-time key disclosure, got %+v", decoded)
	}
}

func TestCreateRouteMapsCPAFailureTo502(t *testing.T) {
	router := newManagementTestRouter(&stubManagementProvider{createErr: service.ErrCPARequestFailed})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys", bytes.NewReader([]byte(`{}`)))
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadGateway {
		t.Fatalf("expected 502, got %d", response.Code)
	}
}

func TestRegenerateRouteMapsDisabledTo409(t *testing.T) {
	router := newManagementTestRouter(&stubManagementProvider{regenErr: service.ErrKeyDisabled})
	response := httptest.NewRecorder()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys/3/regenerate", nil)
	router.ServeHTTP(response, request)
	if response.Code != http.StatusConflict {
		t.Fatalf("expected 409, got %d", response.Code)
	}
}

func TestPolicyRoutesRoundTrip(t *testing.T) {
	provider := &stubManagementProvider{}
	router := newManagementTestRouter(provider)
	save := httptest.NewRecorder()
	body := `{"enabled": true, "limits": [{"type":"cost","window":"weekly","value": 100}]}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/usage/api-keys/9/policy", bytes.NewReader([]byte(body)))
	router.ServeHTTP(save, request)
	if save.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", save.Code, save.Body.String())
	}
	if len(provider.limitsIn) != 1 || provider.limitsIn[0].Value != 100 {
		t.Fatalf("expected parsed limits, got %+v", provider.limitsIn)
	}
	fetch := httptest.NewRecorder()
	router.ServeHTTP(fetch, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys/9/policy", nil))
	if fetch.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", fetch.Code)
	}
	logs := httptest.NewRecorder()
	router.ServeHTTP(logs, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys/9/enforcement-logs", nil))
	if logs.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", logs.Code)
	}
}

func TestPolicyRouteRejectsInvalidLimits(t *testing.T) {
	router := newManagementTestRouter(&stubManagementProvider{})
	response := httptest.NewRecorder()
	body := `{"enabled": true, "limits": [{"type":"cost","window":"daily","value": -5}]}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/usage/api-keys/9/policy", bytes.NewReader([]byte(body)))
	router.ServeHTTP(response, request)
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", response.Code)
	}
}

// TestManagementRouteMapsUnknownErrorsTo500 验证非哨兵错误按基础设施故障处理：
// 返回统一 500 文案，不把内部错误细节泄漏给客户端。
func TestManagementRouteMapsUnknownErrorsTo500(t *testing.T) {
	router := newManagementTestRouter(&stubManagementProvider{saveErr: errors.New("database exploded")})
	response := httptest.NewRecorder()
	body := `{"enabled": true, "limits": []}`
	request := httptest.NewRequest(http.MethodPut, "/api/v1/usage/api-keys/9/policy", bytes.NewReader([]byte(body)))
	router.ServeHTTP(response, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("expected 500, got %d", response.Code)
	}
	if strings.Contains(response.Body.String(), "database exploded") {
		t.Fatalf("internal error detail must not leak, got %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), "internal server error") {
		t.Fatalf("expected unified internal error body, got %s", response.Body.String())
	}
}

func TestCPAAPIKeyListRouteAttachesPolicySummaries(t *testing.T) {
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyRoutes(group, stubCPAAPIKeyListProvider{rows: []entities.CPAAPIKey{
		{ID: 1, APIKey: "sk-alpha123456"},
		{ID: 2, APIKey: "sk-beta654321"},
	}}, &stubManagementProvider{summaries: map[int64]service.CPAAPIKeyPolicySummary{
		1: {
			EnforcementState: "active",
			Tightest: &keypolicy.TightestLimit{
				Limit: keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 100},
				Used:  40,
				Ratio: 0.4,
			},
		},
	}})

	// 展示列表与设置列表走同一套徽标附加逻辑，两个端点的行都必须携带 policy。
	for _, path := range []string{"/api/v1/usage/api-keys", "/api/v1/usage/api-keys/settings"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", path, response.Code, response.Body.String())
		}
		var parsed struct {
			Items []struct {
				APIKey string `json:"apiKey"`
				Policy *struct {
					Enabled          bool   `json:"enabled"`
					EnforcementState string `json:"enforcementState"`
					Tightest         *struct {
						Type  string  `json:"type"`
						Value float64 `json:"value"`
						Used  float64 `json:"used"`
						Ratio float64 `json:"ratio"`
					} `json:"tightest"`
				} `json:"policy"`
			} `json:"items"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil {
			t.Fatalf("%s: decode response: %v", path, err)
		}
		if len(parsed.Items) != 2 {
			t.Fatalf("%s: expected two rows, got %+v", path, parsed.Items)
		}
		if parsed.Items[0].Policy == nil || !parsed.Items[0].Policy.Enabled || parsed.Items[0].Policy.Tightest == nil {
			t.Fatalf("%s: expected enriched policy summary on first row, got %+v", path, parsed.Items[0].Policy)
		}
		if parsed.Items[0].Policy.Tightest.Value != 100 || parsed.Items[0].Policy.Tightest.Used != 40 || parsed.Items[0].Policy.Tightest.Ratio != 0.4 {
			t.Fatalf("%s: expected tightest limit detail, got %+v", path, parsed.Items[0].Policy.Tightest)
		}
		// 没有策略行的 key 也输出 enabled=true 的默认徽标：限额强制常开，没有策略行只是没有限额。
		if parsed.Items[1].Policy == nil || !parsed.Items[1].Policy.Enabled || parsed.Items[1].Policy.EnforcementState != "active" || parsed.Items[1].Policy.Tightest != nil {
			t.Fatalf("%s: expected default policy summary on second row, got %+v", path, parsed.Items[1].Policy)
		}
	}
}

func TestCPAAPIKeyListRouteOmitsPolicyWhenSummaryFails(t *testing.T) {
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyRoutes(group, stubCPAAPIKeyListProvider{rows: []entities.CPAAPIKey{
		{ID: 1, APIKey: "sk-alpha123456"},
	}}, &stubManagementProvider{summariesErr: errors.New("summary boom")})

	for _, path := range []string{"/api/v1/usage/api-keys", "/api/v1/usage/api-keys/settings"} {
		response := httptest.NewRecorder()
		router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", path, response.Code, response.Body.String())
		}
		// 汇总失败只降级徽标，不阻断列表，也不输出半空的 policy 字段。
		if strings.Contains(response.Body.String(), `"policy"`) {
			t.Fatalf("%s: expected policy field to be omitted, got %s", path, response.Body.String())
		}
	}
}

func TestQuotaResetRouteRequiresExplicitScope(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		wantStatus int
		wantIDs    []int64
	}{
		{name: "all keys", body: `{"allKeys":true,"windows":["weekly"]}`, wantStatus: http.StatusNoContent},
		{name: "selected keys", body: `{"apiKeyIds":["9007199254740993","7"],"windows":["daily","monthly"]}`, wantStatus: http.StatusNoContent, wantIDs: []int64{9007199254740993, 7}},
		{name: "neither", body: `{"windows":["weekly"]}`, wantStatus: http.StatusBadRequest},
		{name: "both", body: `{"allKeys":true,"apiKeyIds":["7"],"windows":["weekly"]}`, wantStatus: http.StatusBadRequest},
		{name: "bad id", body: `{"apiKeyIds":["abc"],"windows":["weekly"]}`, wantStatus: http.StatusBadRequest},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			provider := &stubManagementProvider{}
			response := httptest.NewRecorder()
			newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys/quota-resets", strings.NewReader(tc.body)))
			if response.Code != tc.wantStatus {
				t.Fatalf("expected %d, got %d: %s", tc.wantStatus, response.Code, response.Body.String())
			}
			if tc.wantStatus == http.StatusNoContent && fmt.Sprint(provider.resetIDs) != fmt.Sprint(tc.wantIDs) && !(len(tc.wantIDs) == 0 && len(provider.resetIDs) == 0) {
				t.Fatalf("expected ids %v, got %v", tc.wantIDs, provider.resetIDs)
			}
		})
	}
}

func TestBulkPolicyRouteKeepsNullAsRemoval(t *testing.T) {
	provider := &stubManagementProvider{}
	response := httptest.NewRecorder()
	body := `{"apiKeyIds":["3","4"],"limits":{"daily":5,"monthly":null}}`
	newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/v1/usage/api-keys/policies", strings.NewReader(body)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
	}
	daily, hasDaily := provider.updateChanges.Limits[keypolicy.LimitWindowDaily]
	monthly, hasMonthly := provider.updateChanges.Limits[keypolicy.LimitWindowMonthly]
	if fmt.Sprint(provider.updateIDs) != "[3 4]" || !hasDaily || daily == nil || *daily != 5 || !hasMonthly || monthly != nil {
		t.Fatalf("unexpected bulk policy call: ids=%v changes=%+v", provider.updateIDs, provider.updateChanges)
	}
	if _, hasWeekly := provider.updateChanges.Limits[keypolicy.LimitWindowWeekly]; hasWeekly {
		t.Fatalf("weekly was not requested and must stay untouched")
	}
}

// openBulkPolicyTestDB 开一个真实迁移过的临时 sqlite 库，供下面的端到端用例验证落库结果；
// stubManagementProvider 不持久化，验证"行 enabled=1"必须经过真实服务。
func openBulkPolicyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "cpa_api_keys_policies_bulk.db")
	db, err := repository.OpenDatabase(config.Config{SQLitePath: dbPath})
	if err != nil {
		t.Fatalf("open bulk policy test database: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatalf("load bulk policy test sql database: %v", err)
	}
	t.Cleanup(func() {
		if err := sqlDB.Close(); err != nil {
			t.Fatalf("close bulk policy test database: %v", err)
		}
	})
	return db
}

// TestBulkPolicyRouteRejectsEnabledOnlyRequest 验证 enabled 字段从请求体移除后，
// 只传 apiKeyIds + enabled、没有 limits 的 PATCH 已经无事可做，必须是 400。
func TestBulkPolicyRouteRejectsEnabledOnlyRequest(t *testing.T) {
	db := openBulkPolicyTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-bulk-reject", DisplayKey: "sk-bulk-reject"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	response := httptest.NewRecorder()
	body := fmt.Sprintf(`{"apiKeyIds":["%d"],"enabled":false}`, key.ID)
	newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/v1/usage/api-keys/policies", strings.NewReader(body)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

// TestBulkPolicyRouteIgnoresEnabledAndForcesRowOn 验证一个仍发送 enabled: false 的旧前端不会
// 关掉限额开关：limits 之外的 enabled 被忽略，且落库的 enabled 始终是 1。
func TestBulkPolicyRouteIgnoresEnabledAndForcesRowOn(t *testing.T) {
	db := openBulkPolicyTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-bulk-force-on", DisplayKey: "sk-bulk-force-on"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	// 模拟迁移前残留的 enabled = 0 行，证明 PATCH 会把它纠正回 1。
	legacy := entities.CPAAPIKeyPolicy{CPAAPIKeyID: key.ID, Limits: "[]", Enabled: false, EnforcementState: string(keypolicy.StateActive)}
	if err := repository.UpsertCPAAPIKeyPolicy(db, &legacy); err != nil {
		t.Fatalf("seed legacy disabled policy: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	response := httptest.NewRecorder()
	body := fmt.Sprintf(`{"apiKeyIds":["%d"],"limits":{"daily":5},"enabled":false}`, key.ID)
	newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodPatch, "/api/v1/usage/api-keys/policies", strings.NewReader(body)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, key.ID)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if !policy.Enabled {
		t.Fatalf("expected enabled forced to 1 regardless of request body, got %+v", policy)
	}
}

// TestSinglePolicyRouteIgnoresEnabledAndForcesRowOn 验证单 key 的 PUT .../policy 同样忽略
// enabled 字段并把行强制写成 enabled = 1，即便请求体里带着 enabled: false。
func TestSinglePolicyRouteIgnoresEnabledAndForcesRowOn(t *testing.T) {
	db := openBulkPolicyTestDB(t)
	key := entities.CPAAPIKey{APIKey: "sk-single-force-on", DisplayKey: "sk-single-force-on"}
	if err := db.Create(&key).Error; err != nil {
		t.Fatalf("seed key: %v", err)
	}
	legacy := entities.CPAAPIKeyPolicy{CPAAPIKeyID: key.ID, Limits: "[]", Enabled: false, EnforcementState: string(keypolicy.StateActive)}
	if err := repository.UpsertCPAAPIKeyPolicy(db, &legacy); err != nil {
		t.Fatalf("seed legacy disabled policy: %v", err)
	}
	provider := service.NewCPAAPIKeyManagementService(db, nil, nil, nil)
	response := httptest.NewRecorder()
	body := fmt.Sprintf(`{"enabled":false,"limits":[{"type":"cost","window":"daily","value":5}]}`)
	request := httptest.NewRequest(http.MethodPut, fmt.Sprintf("/api/v1/usage/api-keys/%d/policy", key.ID), strings.NewReader(body))
	newManagementTestRouter(provider).ServeHTTP(response, request)
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
	}
	policy, err := repository.FindCPAAPIKeyPolicy(db, key.ID)
	if err != nil {
		t.Fatalf("load policy: %v", err)
	}
	if !policy.Enabled {
		t.Fatalf("expected enabled forced to 1 regardless of request body, got %+v", policy)
	}
}

// TestQuotaAdjustmentRouteAppliedSkippedJSONShape 验证 POST 的成功响应用字符串 id 回显
// applied/skipped 两个列表，字段名与设计文档一致。
func TestQuotaAdjustmentRouteAppliedSkippedJSONShape(t *testing.T) {
	provider := &stubManagementProvider{setAdjResult: service.QuotaAdjustmentResult{
		Applied: []service.QuotaAdjustmentTarget{{APIKeyID: 7, Window: keypolicy.LimitWindowWeekly}},
		Skipped: []service.QuotaAdjustmentTarget{{APIKeyID: 9, Window: keypolicy.LimitWindowDaily}},
	}}
	response := httptest.NewRecorder()
	body := `{"apiKeyIds":["7","9"],"windows":["daily","weekly"],"deltaUsd":500}`
	newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys/quota-adjustments", strings.NewReader(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var decoded struct {
		Applied []struct {
			APIKeyID string `json:"apiKeyId"`
			Window   string `json:"window"`
		} `json:"applied"`
		Skipped []struct {
			APIKeyID string `json:"apiKeyId"`
			Window   string `json:"window"`
			Reason   string `json:"reason"`
		} `json:"skipped"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &decoded); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if len(decoded.Applied) != 1 || decoded.Applied[0].APIKeyID != "7" || decoded.Applied[0].Window != "weekly" {
		t.Fatalf("unexpected applied list: %+v", decoded.Applied)
	}
	if len(decoded.Skipped) != 1 || decoded.Skipped[0].APIKeyID != "9" || decoded.Skipped[0].Window != "daily" || decoded.Skipped[0].Reason != "no_limit" {
		t.Fatalf("unexpected skipped list: %+v", decoded.Skipped)
	}
	if fmt.Sprint(provider.setAdjIDs) != "[7 9]" || provider.setAdjDelta != 500 {
		t.Fatalf("expected provider called with parsed ids/delta, got ids=%v delta=%v", provider.setAdjIDs, provider.setAdjDelta)
	}
}

// TestQuotaAdjustmentRouteRejectsZeroDelta 验证 deltaUsd: 0 在服务层被拒绝后路由映射为 400。
func TestQuotaAdjustmentRouteRejectsZeroDelta(t *testing.T) {
	provider := &stubManagementProvider{setAdjErr: service.ErrInvalidInput}
	response := httptest.NewRecorder()
	body := `{"apiKeyIds":["7"],"windows":["weekly"],"deltaUsd":0}`
	newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/usage/api-keys/quota-adjustments", strings.NewReader(body)))
	if response.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d: %s", response.Code, response.Body.String())
	}
}

// TestQuotaAdjustmentRouteDeleteClears 验证 DELETE 把字符串 id 解析后转交服务层，成功返回 204。
func TestQuotaAdjustmentRouteDeleteClears(t *testing.T) {
	provider := &stubManagementProvider{}
	response := httptest.NewRecorder()
	body := `{"apiKeyIds":["7","8"],"windows":["weekly","monthly"]}`
	newManagementTestRouter(provider).ServeHTTP(response, httptest.NewRequest(http.MethodDelete, "/api/v1/usage/api-keys/quota-adjustments", strings.NewReader(body)))
	if response.Code != http.StatusNoContent {
		t.Fatalf("expected 204, got %d: %s", response.Code, response.Body.String())
	}
	if fmt.Sprint(provider.clearAdjIDs) != "[7 8]" {
		t.Fatalf("expected parsed ids forwarded, got %v", provider.clearAdjIDs)
	}
	if fmt.Sprint(provider.clearAdjWindows) != "[weekly monthly]" {
		t.Fatalf("expected parsed windows forwarded, got %v", provider.clearAdjWindows)
	}
}

// TestCPAAPIKeyListRouteIncludesBaseValueAndAdjustmentUsd 验证列表徽标的 limits 项携带
// baseValue（正常限额）与 adjustmentUsd（当前生效的 delta，没有调整时整体省略）。
func TestCPAAPIKeyListRouteIncludesBaseValueAndAdjustmentUsd(t *testing.T) {
	delta := 500.0
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyRoutes(group, stubCPAAPIKeyListProvider{rows: []entities.CPAAPIKey{
		{ID: 1, APIKey: "sk-alpha123456"},
	}}, &stubManagementProvider{summaries: map[int64]service.CPAAPIKeyPolicySummary{
		1: {
			EnforcementState: "active",
			Limits: []keypolicy.TightestLimit{
				{Limit: keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowWeekly, Value: 600}, Used: 40, Ratio: 40.0 / 600, Base: 100, Adjustment: &delta},
			},
		},
	}})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"baseValue":100`) {
		t.Fatalf("expected baseValue in response, got %s", response.Body.String())
	}
	if !strings.Contains(response.Body.String(), `"adjustmentUsd":500`) {
		t.Fatalf("expected adjustmentUsd in response, got %s", response.Body.String())
	}

	var parsed struct {
		Items []struct {
			Policy struct {
				Limits []struct {
					BaseValue     float64  `json:"baseValue"`
					AdjustmentUSD *float64 `json:"adjustmentUsd"`
				} `json:"limits"`
			} `json:"policy"`
		} `json:"items"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &parsed); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	limits := parsed.Items[0].Policy.Limits
	if len(limits) != 1 || limits[0].BaseValue != 100 || limits[0].AdjustmentUSD == nil || *limits[0].AdjustmentUSD != 500 {
		t.Fatalf("unexpected limit item: %+v", limits)
	}
}

// TestCPAAPIKeyListRouteOmitsAdjustmentUsdWhenNoAdjustment 验证没有生效调整时 adjustmentUsd
// 字段整体省略（omitempty），而 baseValue 仍然等于限额本身。
func TestCPAAPIKeyListRouteOmitsAdjustmentUsdWhenNoAdjustment(t *testing.T) {
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyRoutes(group, stubCPAAPIKeyListProvider{rows: []entities.CPAAPIKey{
		{ID: 1, APIKey: "sk-alpha123456"},
	}}, &stubManagementProvider{summaries: map[int64]service.CPAAPIKeyPolicySummary{
		1: {
			EnforcementState: "active",
			Limits: []keypolicy.TightestLimit{
				{Limit: keypolicy.Limit{Type: keypolicy.LimitTypeCost, Window: keypolicy.LimitWindowDaily, Value: 100}, Used: 10, Ratio: 0.1, Base: 100},
			},
		},
	}})
	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	if strings.Contains(response.Body.String(), "adjustmentUsd") {
		t.Fatalf("expected adjustmentUsd omitted without an active adjustment, got %s", response.Body.String())
	}
}
