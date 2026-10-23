package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"unicode"

	"cpa-usage-keeper/internal/entities"
	"cpa-usage-keeper/internal/helper"
	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/service"
	"cpa-usage-keeper/internal/timeutil"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

const maxCPAAPIKeyAliasLength = 128

type cpaAPIKeyResponse struct {
	ID           string  `json:"id"`
	KeyAlias     string  `json:"keyAlias"`
	DisplayKey   string  `json:"displayKey"`
	Label        string  `json:"label"`
	LastSyncedAt *string `json:"lastSyncedAt"`
	// Policy 是列表徽标用的策略摘要；管理服务不可用或汇总失败时省略。
	Policy *cpaAPIKeyPolicySummaryResponse `json:"policy,omitempty"`
}

type cpaAPIKeyListResponse struct {
	Items []cpaAPIKeyResponse `json:"items"`
}

type cpaAPIKeySettingsResponse struct {
	ID           string  `json:"id"`
	APIKey       string  `json:"apiKey"`
	KeyAlias     string  `json:"keyAlias"`
	DisplayKey   string  `json:"displayKey"`
	Label        string  `json:"label"`
	LastSyncedAt *string `json:"lastSyncedAt"`
	// Policy 与展示列表一致：徽标用策略摘要；管理服务不可用或汇总失败时省略。
	Policy *cpaAPIKeyPolicySummaryResponse `json:"policy,omitempty"`
}

type cpaAPIKeySettingsListResponse struct {
	Items []cpaAPIKeySettingsResponse `json:"items"`
}

type cpaAPIKeyOption struct {
	ID    string `json:"id"`
	Label string `json:"label"`
}

type cpaAPIKeyOptionsResponse struct {
	Options []cpaAPIKeyOption `json:"options"`
}

type updateCPAAPIKeyAliasRequest struct {
	KeyAlias string `json:"keyAlias"`
}

func registerCPAAPIKeyRoutes(router gin.IRoutes, provider service.CPAAPIKeyProvider, managementProvider service.CPAAPIKeyManagementProvider) {
	router.GET("/usage/api-keys", func(c *gin.Context) {
		rows, err := listCPAAPIKeyRows(c, provider, managementProvider)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, cpaAPIKeyListResponse{Items: rows})
	})

	router.GET("/usage/api-keys/settings", func(c *gin.Context) {
		rows, err := listCPAAPIKeySettingsRows(c, provider, managementProvider)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, cpaAPIKeySettingsListResponse{Items: rows})
	})

	router.GET("/usage/api-keys/options", func(c *gin.Context) {
		rows, err := listCPAAPIKeyOptionRows(c, provider)
		if err != nil {
			return
		}
		c.JSON(http.StatusOK, cpaAPIKeyOptionsResponse{Options: rows})
	})

	router.PATCH("/usage/api-keys/:id", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key provider is not configured"})
			return
		}
		id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
		if err != nil || id <= 0 {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
			return
		}
		var request updateCPAAPIKeyAliasRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		request.KeyAlias = strings.TrimSpace(request.KeyAlias)
		if err := validateCPAAPIKeyAlias(request.KeyAlias); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		row, err := provider.UpdateCPAAPIKeyAlias(c.Request.Context(), id, request.KeyAlias)
		if err != nil {
			if errors.Is(err, service.ErrInvalidID) {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
				return
			}
			if errors.Is(err, gorm.ErrRecordNotFound) {
				c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
				return
			}
			writeInternalError(c, "update api key alias failed", err)
			return
		}
		c.JSON(http.StatusOK, toCPAAPIKeyResponse(row))
	})
}

// fetchCPAAPIKeyPolicySummaries 拉取策略摘要映射；管理服务未配置或汇总失败时返回 nil，
// 调用方据此整体省略 policy 字段（徽标降级，绝不拖垮列表本身）。
func fetchCPAAPIKeyPolicySummaries(ctx context.Context, managementProvider service.CPAAPIKeyManagementProvider) map[int64]service.CPAAPIKeyPolicySummary {
	if managementProvider == nil {
		return nil
	}
	summaries, err := managementProvider.ListCPAAPIKeyPolicySummaries(ctx)
	if err != nil {
		// 徽标静默降级必须可观测：列表照常返回，但告警日志留痕。
		logrus.WithError(err).Warn("load api key policy summaries failed")
		return nil
	}
	return summaries
}

// attachCPAAPIKeyPolicySummaries 是两个列表端点共用的行构建器：基础行转换后统一附加策略徽标。
// summaries 为 nil 时省略 policy；没有摘要行的 key（没有策略行，而不是策略被关闭）输出默认徽标。
func attachCPAAPIKeyPolicySummaries[T any](rows []entities.CPAAPIKey, summaries map[int64]service.CPAAPIKeyPolicySummary, toRow func(entities.CPAAPIKey, *cpaAPIKeyPolicySummaryResponse) T) []T {
	response := make([]T, 0, len(rows))
	for _, row := range rows {
		policy := (*cpaAPIKeyPolicySummaryResponse)(nil)
		if summaries != nil {
			summary, ok := summaries[row.ID]
			if !ok {
				// 没有策略行的 key 也在常开之列，只是没有限额；默认徽标同样是 enabled=true。
				summary = service.CPAAPIKeyPolicySummary{EnforcementState: string(keypolicy.StateActive)}
			}
			policy = toCPAAPIKeyPolicySummaryResponse(summary)
		}
		response = append(response, toRow(row, policy))
	}
	return response
}

func listCPAAPIKeyRows(c *gin.Context, provider service.CPAAPIKeyProvider, managementProvider service.CPAAPIKeyManagementProvider) ([]cpaAPIKeyResponse, error) {
	if provider == nil {
		return []cpaAPIKeyResponse{}, nil
	}
	rows, err := provider.ListCPAAPIKeys(c.Request.Context())
	if err != nil {
		writeInternalError(c, "list api keys failed", err)
		return nil, err
	}
	summaries := fetchCPAAPIKeyPolicySummaries(c.Request.Context(), managementProvider)
	return attachCPAAPIKeyPolicySummaries(rows, summaries, func(row entities.CPAAPIKey, policy *cpaAPIKeyPolicySummaryResponse) cpaAPIKeyResponse {
		item := toCPAAPIKeyResponse(row)
		item.Policy = policy
		return item
	}), nil
}

func listCPAAPIKeySettingsRows(c *gin.Context, provider service.CPAAPIKeyProvider, managementProvider service.CPAAPIKeyManagementProvider) ([]cpaAPIKeySettingsResponse, error) {
	if provider == nil {
		return []cpaAPIKeySettingsResponse{}, nil
	}
	rows, err := provider.ListCPAAPIKeys(c.Request.Context())
	if err != nil {
		writeInternalError(c, "list api key settings failed", err)
		return nil, err
	}
	summaries := fetchCPAAPIKeyPolicySummaries(c.Request.Context(), managementProvider)
	return attachCPAAPIKeyPolicySummaries(rows, summaries, func(row entities.CPAAPIKey, policy *cpaAPIKeyPolicySummaryResponse) cpaAPIKeySettingsResponse {
		item := toCPAAPIKeySettingsResponse(row)
		item.Policy = policy
		return item
	}), nil
}

func listCPAAPIKeyOptionRows(c *gin.Context, provider service.CPAAPIKeyProvider) ([]cpaAPIKeyOption, error) {
	if provider == nil {
		return []cpaAPIKeyOption{}, nil
	}
	rows, err := provider.ListCPAAPIKeys(c.Request.Context())
	if err != nil {
		writeInternalError(c, "list api key options failed", err)
		return nil, err
	}
	response := make([]cpaAPIKeyOption, 0, len(rows))
	for _, row := range rows {
		response = append(response, toCPAAPIKeyOption(row))
	}
	return response, nil
}

func toCPAAPIKeyResponse(row entities.CPAAPIKey) cpaAPIKeyResponse {
	label := helper.CPAAPIKeyDisplayName(row)
	var lastSyncedAt *string
	if row.LastSyncedAt != nil {
		value := timeutil.FormatStorageTime(*row.LastSyncedAt)
		lastSyncedAt = &value
	}
	return cpaAPIKeyResponse{
		ID:           strconv.FormatInt(row.ID, 10),
		KeyAlias:     row.KeyAlias,
		DisplayKey:   helper.CPAAPIKeyMaskedDisplayKey(row),
		Label:        label,
		LastSyncedAt: lastSyncedAt,
	}
}

func toCPAAPIKeySettingsResponse(row entities.CPAAPIKey) cpaAPIKeySettingsResponse {
	label := helper.CPAAPIKeyDisplayName(row)
	var lastSyncedAt *string
	if row.LastSyncedAt != nil {
		value := timeutil.FormatStorageTime(*row.LastSyncedAt)
		lastSyncedAt = &value
	}
	return cpaAPIKeySettingsResponse{
		ID:           strconv.FormatInt(row.ID, 10),
		APIKey:       row.APIKey,
		KeyAlias:     row.KeyAlias,
		DisplayKey:   helper.CPAAPIKeyMaskedDisplayKey(row),
		Label:        label,
		LastSyncedAt: lastSyncedAt,
	}
}

func toCPAAPIKeyOption(row entities.CPAAPIKey) cpaAPIKeyOption {
	label := helper.CPAAPIKeyDisplayName(row)
	return cpaAPIKeyOption{
		ID:    strconv.FormatInt(row.ID, 10),
		Label: label,
	}
}

func validateCPAAPIKeyAlias(value string) error {
	if len([]rune(value)) > maxCPAAPIKeyAliasLength {
		return errors.New("keyAlias is too long")
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return errors.New("keyAlias cannot contain control characters")
		}
	}
	return nil
}

type createCPAAPIKeyRequest struct {
	KeyAlias string `json:"keyAlias"`
	Key      string `json:"key"`
}

type createdCPAAPIKeyResponse struct {
	ID       string `json:"id"`
	Key      string `json:"key"`
	KeyAlias string `json:"keyAlias"`
}

// saveCPAAPIKeyPolicyRequest：limits 外不再接受 enabled；限额开关已固定常开，
// 旧前端若仍发送 enabled 字段，标准 JSON 解码会直接忽略未知字段，不会报错。
type saveCPAAPIKeyPolicyRequest struct {
	Limits []keypolicy.Limit `json:"limits"`
}

// resetCPAAPIKeyQuotasRequest：allKeys 为 true 时全局清零，否则 apiKeyIds 必填。
// 显式 allKeys 防止前端漏传 id 时误伤所有 key。
type resetCPAAPIKeyQuotasRequest struct {
	AllKeys   bool     `json:"allKeys"`
	APIKeyIDs []string `json:"apiKeyIds"`
	Windows   []string `json:"windows"`
}

// updateCPAAPIKeyPoliciesRequest：limits 只列要改的窗口，值为 null 表示移除。
// 不再接受 enabled：限额开关已固定常开，旧前端发来的 enabled 字段被静默忽略。
type updateCPAAPIKeyPoliciesRequest struct {
	APIKeyIDs []string            `json:"apiKeyIds"`
	Limits    map[string]*float64 `json:"limits"`
}

// setCPAAPIKeyQuotaAdjustmentsRequest：deltaUsd 是带符号的调整额（美元），只对已有
// 正常限额的窗口生效；没有限额的窗口会被跳过并体现在响应的 skipped 列表里。
type setCPAAPIKeyQuotaAdjustmentsRequest struct {
	APIKeyIDs []string `json:"apiKeyIds"`
	Windows   []string `json:"windows"`
	DeltaUSD  float64  `json:"deltaUsd"`
}

// clearCPAAPIKeyQuotaAdjustmentsRequest 提前清除所选 key × 窗口当前生效的临时调整。
type clearCPAAPIKeyQuotaAdjustmentsRequest struct {
	APIKeyIDs []string `json:"apiKeyIds"`
	Windows   []string `json:"windows"`
}

type cpaAPIKeyQuotaAdjustmentTargetResponse struct {
	APIKeyID string `json:"apiKeyId"`
	Window   string `json:"window"`
}

type cpaAPIKeyQuotaAdjustmentSkippedResponse struct {
	APIKeyID string `json:"apiKeyId"`
	Window   string `json:"window"`
	Reason   string `json:"reason"`
}

type cpaAPIKeyQuotaAdjustmentResultResponse struct {
	Applied []cpaAPIKeyQuotaAdjustmentTargetResponse  `json:"applied"`
	Skipped []cpaAPIKeyQuotaAdjustmentSkippedResponse `json:"skipped"`
}

// toCPAAPIKeyQuotaAdjustmentResultResponse 把服务层结果转成字符串 id 的响应；
// skipped 的 reason 固定是 "no_limit"：该 key 在这个窗口本来就没有正常限额。
func toCPAAPIKeyQuotaAdjustmentResultResponse(result service.QuotaAdjustmentResult) cpaAPIKeyQuotaAdjustmentResultResponse {
	applied := make([]cpaAPIKeyQuotaAdjustmentTargetResponse, 0, len(result.Applied))
	for _, target := range result.Applied {
		applied = append(applied, cpaAPIKeyQuotaAdjustmentTargetResponse{
			APIKeyID: strconv.FormatInt(target.APIKeyID, 10), Window: string(target.Window),
		})
	}
	skipped := make([]cpaAPIKeyQuotaAdjustmentSkippedResponse, 0, len(result.Skipped))
	for _, target := range result.Skipped {
		skipped = append(skipped, cpaAPIKeyQuotaAdjustmentSkippedResponse{
			APIKeyID: strconv.FormatInt(target.APIKeyID, 10), Window: string(target.Window), Reason: "no_limit",
		})
	}
	return cpaAPIKeyQuotaAdjustmentResultResponse{Applied: applied, Skipped: skipped}
}

// parseCPAAPIKeyIDList 解析请求体中的字符串 id 列表（id 可能超出 JS 安全整数范围）。
func parseCPAAPIKeyIDList(raw []string) ([]int64, bool) {
	ids := make([]int64, 0, len(raw))
	for _, item := range raw {
		id, err := strconv.ParseInt(strings.TrimSpace(item), 10, 64)
		if err != nil || id <= 0 {
			return nil, false
		}
		ids = append(ids, id)
	}
	return ids, true
}

type cpaAPIKeyPolicyUsageWindowResponse struct {
	CostUSD float64 `json:"costUsd"`
}

type cpaAPIKeyPolicyUsageResponse struct {
	Daily   cpaAPIKeyPolicyUsageWindowResponse `json:"daily"`
	Weekly  cpaAPIKeyPolicyUsageWindowResponse `json:"weekly"`
	Monthly cpaAPIKeyPolicyUsageWindowResponse `json:"monthly"`
}

type cpaAPIKeyTightestLimitResponse struct {
	Type   string  `json:"type"`
	Window string  `json:"window"`
	Value  float64 `json:"value"`
	Used   float64 `json:"used"`
	Ratio  float64 `json:"ratio"`
	// BaseValue 是正常存储的限额；没有临时调整时等于 Value。
	BaseValue float64 `json:"baseValue"`
	// AdjustmentUSD 是当前生效的临时调整额；没有调整时整体省略，前端据此判断是否展示 "temp" 标记。
	AdjustmentUSD *float64 `json:"adjustmentUsd,omitempty"`
}

type cpaAPIKeyPolicySummaryResponse struct {
	Enabled          bool                             `json:"enabled"`
	EnforcementState string                           `json:"enforcementState"`
	Tightest         *cpaAPIKeyTightestLimitResponse  `json:"tightest"`
	Limits           []cpaAPIKeyTightestLimitResponse `json:"limits"`
}

type cpaAPIKeyPolicyResponse struct {
	Enabled          bool                            `json:"enabled"`
	EnforcementState string                          `json:"enforcementState"`
	AdminDisabled    bool                            `json:"adminDisabled"`
	Limits           []keypolicy.Limit               `json:"limits"`
	Usage            cpaAPIKeyPolicyUsageResponse    `json:"usage"`
	Tightest         *cpaAPIKeyTightestLimitResponse `json:"tightest"`
}

type cpaAPIKeyEnforcementLogResponse struct {
	ID         string   `json:"id"`
	Action     string   `json:"action"`
	Reason     string   `json:"reason"`
	LimitType  *string  `json:"limitType"`
	Window     *string  `json:"window"`
	UsedValue  *float64 `json:"usedValue"`
	LimitValue *float64 `json:"limitValue"`
	Detail     string   `json:"detail"`
	CreatedAt  string   `json:"createdAt"`
}

type cpaAPIKeyEnforcementLogsResponse struct {
	Items []cpaAPIKeyEnforcementLogResponse `json:"items"`
}

// parseCPAAPIKeyID 统一解析并校验路由参数 id。
func parseCPAAPIKeyID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(strings.TrimSpace(c.Param("id")), 10, 64)
	if err != nil || id <= 0 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
		return 0, false
	}
	return id, true
}

// writeManagementError 把服务层错误映射到固定的 HTTP 语义。
func writeManagementError(c *gin.Context, message string, err error) {
	switch {
	case errors.Is(err, service.ErrCPARequestFailed):
		c.JSON(http.StatusBadGateway, gin.H{"error": "cpa api request failed"})
	case errors.Is(err, service.ErrKeyDisabled):
		c.JSON(http.StatusConflict, gin.H{"error": "api key is disabled"})
	case errors.Is(err, service.ErrInvalidID):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
	case errors.Is(err, service.ErrInvalidInput):
		// 校验类错误（限额非法、自定义 key 非法、key 重复）带哨兵，原文可安全返回 400。
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		// 其余未知错误按基础设施故障处理：记录日志并返回统一 500，不向客户端泄漏内部细节。
		logrus.WithError(err).Error(message)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func registerCPAAPIKeyManagementRoutes(router gin.IRoutes, provider service.CPAAPIKeyManagementProvider) {
	router.POST("/usage/api-keys", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		var request createCPAAPIKeyRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if err := validateCPAAPIKeyAlias(strings.TrimSpace(request.KeyAlias)); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}
		row, fullKey, err := provider.CreateCPAAPIKey(c.Request.Context(), strings.TrimSpace(request.KeyAlias), request.Key)
		if err != nil {
			writeManagementError(c, "create api key failed", err)
			return
		}
		c.JSON(http.StatusOK, createdCPAAPIKeyResponse{
			ID: strconv.FormatInt(row.ID, 10), Key: fullKey, KeyAlias: row.KeyAlias,
		})
	})

	router.POST("/usage/api-keys/:id/regenerate", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		id, ok := parseCPAAPIKeyID(c)
		if !ok {
			return
		}
		row, fullKey, err := provider.RegenerateCPAAPIKey(c.Request.Context(), id)
		if err != nil {
			writeManagementError(c, "regenerate api key failed", err)
			return
		}
		c.JSON(http.StatusOK, createdCPAAPIKeyResponse{
			ID: strconv.FormatInt(row.ID, 10), Key: fullKey, KeyAlias: row.KeyAlias,
		})
	})

	router.DELETE("/usage/api-keys/:id", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		id, ok := parseCPAAPIKeyID(c)
		if !ok {
			return
		}
		if err := provider.DeleteCPAAPIKey(c.Request.Context(), id); err != nil {
			writeManagementError(c, "delete api key failed", err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	registerCPAAPIKeyToggleRoutes(router, provider)
	registerCPAAPIKeyPolicyRoutes(router, provider)
}

func registerCPAAPIKeyToggleRoutes(router gin.IRoutes, provider service.CPAAPIKeyManagementProvider) {
	handle := func(action string, call func(ctx context.Context, id int64) error) gin.HandlerFunc {
		return func(c *gin.Context) {
			if provider == nil {
				c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
				return
			}
			id, ok := parseCPAAPIKeyID(c)
			if !ok {
				return
			}
			if err := call(c.Request.Context(), id); err != nil {
				writeManagementError(c, action+" api key failed", err)
				return
			}
			c.Status(http.StatusNoContent)
		}
	}
	// 不能直接取 provider 的方法表达式：provider 为 nil 时注册阶段就会解引用空接口。
	router.POST("/usage/api-keys/:id/disable", handle("disable", func(ctx context.Context, id int64) error {
		return provider.DisableCPAAPIKey(ctx, id)
	}))
	router.POST("/usage/api-keys/:id/restore", handle("restore", func(ctx context.Context, id int64) error {
		return provider.RestoreCPAAPIKey(ctx, id)
	}))
}

func toCPAAPIKeyPolicyUsageResponse(usage keypolicy.UsageByWindow) cpaAPIKeyPolicyUsageResponse {
	toWindow := func(w keypolicy.LimitWindow) cpaAPIKeyPolicyUsageWindowResponse {
		return cpaAPIKeyPolicyUsageWindowResponse{CostUSD: usage[w].CostUSD}
	}
	return cpaAPIKeyPolicyUsageResponse{
		Daily:   toWindow(keypolicy.LimitWindowDaily),
		Weekly:  toWindow(keypolicy.LimitWindowWeekly),
		Monthly: toWindow(keypolicy.LimitWindowMonthly),
	}
}

// toCPAAPIKeyTightestLimitResponse 把最紧张限额领域对象转成 UI 进度条响应；nil 原样透传。
func toCPAAPIKeyTightestLimitResponse(tightest *keypolicy.TightestLimit) *cpaAPIKeyTightestLimitResponse {
	if tightest == nil {
		return nil
	}
	return &cpaAPIKeyTightestLimitResponse{
		Type: string(tightest.Limit.Type), Window: string(tightest.Limit.Window),
		Value: tightest.Limit.Value, Used: tightest.Used, Ratio: tightest.Ratio,
		BaseValue: tightest.Base, AdjustmentUSD: tightest.Adjustment,
	}
}

func toCPAAPIKeyPolicySummaryResponse(summary service.CPAAPIKeyPolicySummary) *cpaAPIKeyPolicySummaryResponse {
	limits := make([]cpaAPIKeyTightestLimitResponse, 0, len(summary.Limits))
	for index := range summary.Limits {
		limits = append(limits, *toCPAAPIKeyTightestLimitResponse(&summary.Limits[index]))
	}
	return &cpaAPIKeyPolicySummaryResponse{
		// enabled 固定 true：限额开关已常开，响应字段只为兼容缓存的旧前端而保留。
		Enabled: true, EnforcementState: summary.EnforcementState,
		Tightest: toCPAAPIKeyTightestLimitResponse(summary.Tightest),
		Limits:   limits,
	}
}

func toCPAAPIKeyPolicyResponse(view service.CPAAPIKeyPolicyView) cpaAPIKeyPolicyResponse {
	limits := view.Limits
	if limits == nil {
		limits = keypolicy.Limits{}
	}
	return cpaAPIKeyPolicyResponse{
		// enabled 固定 true：限额开关已常开，响应字段只为兼容缓存的旧前端而保留。
		Enabled: true, EnforcementState: view.Policy.EnforcementState,
		AdminDisabled: view.Policy.AdminDisabled, Limits: limits,
		Usage: toCPAAPIKeyPolicyUsageResponse(view.Usage), Tightest: toCPAAPIKeyTightestLimitResponse(view.Tightest),
	}
}

func registerCPAAPIKeyPolicyRoutes(router gin.IRoutes, provider service.CPAAPIKeyManagementProvider) {
	router.POST("/usage/api-keys/quota-resets", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		var request resetCPAAPIKeyQuotasRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ids, ok := parseCPAAPIKeyIDList(request.APIKeyIDs)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
			return
		}
		if request.AllKeys == (len(ids) > 0) {
			c.JSON(http.StatusBadRequest, gin.H{"error": "select all keys or at least one api key"})
			return
		}
		windows := make([]keypolicy.LimitWindow, 0, len(request.Windows))
		for _, window := range request.Windows {
			windows = append(windows, keypolicy.LimitWindow(strings.TrimSpace(window)))
		}
		if err := provider.ResetCPAAPIKeyQuotas(c.Request.Context(), ids, windows); err != nil {
			writeManagementError(c, "reset api key quotas failed", err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	router.POST("/usage/api-keys/quota-adjustments", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		var request setCPAAPIKeyQuotaAdjustmentsRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ids, ok := parseCPAAPIKeyIDList(request.APIKeyIDs)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
			return
		}
		windows := make([]keypolicy.LimitWindow, 0, len(request.Windows))
		for _, window := range request.Windows {
			windows = append(windows, keypolicy.LimitWindow(strings.TrimSpace(window)))
		}
		result, err := provider.SetCPAAPIKeyQuotaAdjustments(c.Request.Context(), ids, windows, request.DeltaUSD)
		if err != nil {
			writeManagementError(c, "set api key quota adjustments failed", err)
			return
		}
		c.JSON(http.StatusOK, toCPAAPIKeyQuotaAdjustmentResultResponse(result))
	})

	router.DELETE("/usage/api-keys/quota-adjustments", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		var request clearCPAAPIKeyQuotaAdjustmentsRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ids, ok := parseCPAAPIKeyIDList(request.APIKeyIDs)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
			return
		}
		windows := make([]keypolicy.LimitWindow, 0, len(request.Windows))
		for _, window := range request.Windows {
			windows = append(windows, keypolicy.LimitWindow(strings.TrimSpace(window)))
		}
		if err := provider.ClearCPAAPIKeyQuotaAdjustments(c.Request.Context(), ids, windows); err != nil {
			writeManagementError(c, "clear api key quota adjustments failed", err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	router.PATCH("/usage/api-keys/policies", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		var request updateCPAAPIKeyPoliciesRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ids, ok := parseCPAAPIKeyIDList(request.APIKeyIDs)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
			return
		}
		changes := service.CPAAPIKeyPolicyChanges{Limits: make(map[keypolicy.LimitWindow]*float64, len(request.Limits))}
		for window, value := range request.Limits {
			changes.Limits[keypolicy.LimitWindow(strings.TrimSpace(window))] = value
		}
		if err := provider.UpdateCPAAPIKeyPolicies(c.Request.Context(), ids, changes); err != nil {
			writeManagementError(c, "update api key policies failed", err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	router.GET("/usage/api-keys/:id/policy", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		id, ok := parseCPAAPIKeyID(c)
		if !ok {
			return
		}
		view, err := provider.GetCPAAPIKeyPolicy(c.Request.Context(), id)
		if err != nil {
			writeManagementError(c, "load api key policy failed", err)
			return
		}
		c.JSON(http.StatusOK, toCPAAPIKeyPolicyResponse(view))
	})

	router.PUT("/usage/api-keys/:id/policy", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		id, ok := parseCPAAPIKeyID(c)
		if !ok {
			return
		}
		var request saveCPAAPIKeyPolicyRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		if err := provider.SaveCPAAPIKeyPolicy(c.Request.Context(), id, keypolicy.Limits(request.Limits)); err != nil {
			writeManagementError(c, "save api key policy failed", err)
			return
		}
		c.Status(http.StatusNoContent)
	})

	router.GET("/usage/api-keys/:id/enforcement-logs", func(c *gin.Context) {
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		id, ok := parseCPAAPIKeyID(c)
		if !ok {
			return
		}
		limit, err := strconv.Atoi(strings.TrimSpace(c.DefaultQuery("limit", "50")))
		if err != nil || limit <= 0 {
			limit = 50
		}
		logs, err := provider.ListCPAAPIKeyEnforcementLogs(c.Request.Context(), id, limit)
		if err != nil {
			writeManagementError(c, "list api key enforcement logs failed", err)
			return
		}
		items := make([]cpaAPIKeyEnforcementLogResponse, 0, len(logs))
		for _, log := range logs {
			items = append(items, cpaAPIKeyEnforcementLogResponse{
				ID: strconv.FormatInt(log.ID, 10), Action: log.Action, Reason: log.Reason,
				LimitType: log.LimitType, Window: log.Window, UsedValue: log.UsedValue,
				LimitValue: log.LimitValue, Detail: log.Detail, CreatedAt: timeutil.FormatStorageTime(log.CreatedAt),
			})
		}
		c.JSON(http.StatusOK, cpaAPIKeyEnforcementLogsResponse{Items: items})
	})
}
