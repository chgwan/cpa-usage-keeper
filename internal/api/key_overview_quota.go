package api

import (
	"net/http"

	"cpa-usage-keeper/internal/keypolicy"
	"cpa-usage-keeper/internal/service"

	"github.com/gin-gonic/gin"
)

// keyOverviewQuotaWindowResponse 是 viewer 端单个日历窗口的费用额度视图；
// 未配置限额的窗口只有花费，limit / ratio 整体省略。
type keyOverviewQuotaWindowResponse struct {
	Window  string   `json:"window"`
	CostUSD float64  `json:"costUsd"`
	Limit   *float64 `json:"limit,omitempty"`
	Ratio   *float64 `json:"ratio,omitempty"`
}

// keyOverviewQuotaResponse 只暴露 viewer 自己 key 的花费与费用限额，
// 不携带 enabled / adminDisabled 等管理端字段。
type keyOverviewQuotaResponse struct {
	EnforcementState string                           `json:"enforcementState"`
	Windows          []keyOverviewQuotaWindowResponse `json:"windows"`
}

func toKeyOverviewQuotaResponse(view service.CPAAPIKeyPolicyView) keyOverviewQuotaResponse {
	limitFor := func(window keypolicy.LimitWindow) *keypolicy.Limit {
		for i := range view.Limits {
			if view.Limits[i].Window == window {
				return &view.Limits[i]
			}
		}
		return nil
	}
	windows := make([]keyOverviewQuotaWindowResponse, 0, 3)
	for _, window := range []keypolicy.LimitWindow{keypolicy.LimitWindowDaily, keypolicy.LimitWindowWeekly, keypolicy.LimitWindowMonthly} {
		item := keyOverviewQuotaWindowResponse{Window: string(window), CostUSD: view.Usage[window].CostUSD}
		if limit := limitFor(window); limit != nil {
			value := limit.Value
			ratio := item.CostUSD / value
			item.Limit, item.Ratio = &value, &ratio
		}
		windows = append(windows, item)
	}
	return keyOverviewQuotaResponse{EnforcementState: view.Policy.EnforcementState, Windows: windows}
}

// registerKeyQuotaRoute 给 API Key viewer 暴露自己 key 的费用额度；
// 会话中间件已把 key 绑定到 session，客户端无法查询他人。
func registerKeyQuotaRoute(router gin.IRoutes, management service.CPAAPIKeyManagementProvider) {
	router.GET("/key-overview/quota", func(c *gin.Context) {
		session, _, ok := activeAPIKeyViewerContext(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "authentication required"})
			return
		}
		if management == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "api key management provider is not configured"})
			return
		}
		view, err := management.GetCPAAPIKeyPolicy(c.Request.Context(), session.CPAAPIKeyID)
		if err != nil {
			writeManagementError(c, "load viewer key quota failed", err)
			return
		}
		c.JSON(http.StatusOK, toKeyOverviewQuotaResponse(view))
	})
}
