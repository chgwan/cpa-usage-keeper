package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/keypolicy"
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
func (s *stubManagementProvider) SaveCPAAPIKeyPolicy(_ context.Context, _ int64, limits keypolicy.Limits, _ bool) error {
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

func (s *stubManagementProvider) ListCPAAPIKeyPolicySummaries(_ context.Context) (map[int64]service.CPAAPIKeyPolicySummary, error) {
	if s.summariesErr != nil {
		return nil, s.summariesErr
	}
	return s.summaries, nil
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
	body := `{"enabled": true, "limits": [{"type":"tokens","window":"daily","value": 100}]}`
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
	body := `{"enabled": true, "limits": [{"type":"tokens","window":"daily","value": -5}]}`
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
			Enabled: true, EnforcementState: "active",
			Tightest: &keypolicy.TightestLimit{
				Limit: keypolicy.Limit{Type: keypolicy.LimitTypeTokens, Window: keypolicy.LimitWindowDaily, Value: 100},
				Used:  40,
				Ratio: 0.4,
			},
		},
	}})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	var parsed struct {
		Items []struct {
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
		t.Fatalf("decode response: %v", err)
	}
	if len(parsed.Items) != 2 {
		t.Fatalf("expected two rows, got %+v", parsed.Items)
	}
	if parsed.Items[0].Policy == nil || !parsed.Items[0].Policy.Enabled || parsed.Items[0].Policy.Tightest == nil {
		t.Fatalf("expected enriched policy summary on first row, got %+v", parsed.Items[0].Policy)
	}
	if parsed.Items[0].Policy.Tightest.Value != 100 || parsed.Items[0].Policy.Tightest.Used != 40 || parsed.Items[0].Policy.Tightest.Ratio != 0.4 {
		t.Fatalf("expected tightest limit detail, got %+v", parsed.Items[0].Policy.Tightest)
	}
	// 没有策略行的 key 输出默认徽标：enabled=false、active、无限额。
	if parsed.Items[1].Policy == nil || parsed.Items[1].Policy.Enabled || parsed.Items[1].Policy.EnforcementState != "active" || parsed.Items[1].Policy.Tightest != nil {
		t.Fatalf("expected default policy summary on second row, got %+v", parsed.Items[1].Policy)
	}
}

func TestCPAAPIKeyListRouteOmitsPolicyWhenSummaryFails(t *testing.T) {
	router := gin.New()
	group := router.Group("/api/v1")
	registerCPAAPIKeyRoutes(group, stubCPAAPIKeyListProvider{rows: []entities.CPAAPIKey{
		{ID: 1, APIKey: "sk-alpha123456"},
	}}, &stubManagementProvider{summariesErr: errors.New("summary boom")})

	response := httptest.NewRecorder()
	router.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api/v1/usage/api-keys", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", response.Code, response.Body.String())
	}
	// 汇总失败只降级徽标，不阻断列表，也不输出半空的 policy 字段。
	if strings.Contains(response.Body.String(), `"policy"`) {
		t.Fatalf("expected policy field to be omitted, got %s", response.Body.String())
	}
}
