package api

import (
	"net/http"
	"time"

	"cpa-usage-keeper/internal/quota"
	"cpa-usage-keeper/internal/service"
	"github.com/gin-gonic/gin"
)

type keyClaudeQuotaItem struct {
	Status       quota.RefreshTaskStatus    `json:"status"`
	Quota        []keyClaudeQuotaRow        `json:"quota,omitempty"`
	Subscription *keyClaudeSubscriptionInfo `json:"subscription,omitempty"`
	RefreshedAt  *time.Time                 `json:"refreshed_at,omitempty"`
	ExpiresAt    *time.Time                 `json:"expires_at,omitempty"`
}

type keyClaudeQuotaRow struct {
	Key         string   `json:"key"`
	Label       string   `json:"label,omitempty"`
	UsedPercent *float64 `json:"usedPercent,omitempty"`
	ResetAt     string   `json:"resetAt,omitempty"`
}

type keyClaudeSubscriptionInfo struct {
	Plan string `json:"plan"`
}

type keyClaudeQuotaResponse struct {
	Items []keyClaudeQuotaItem `json:"items"`
}

func registerKeyClaudeQuotaRoute(router gin.IRoutes, identities service.APIKeyClaudeQuotaIdentityProvider, quotas QuotaProvider) {
	router.GET("/key-claude-quotas", func(c *gin.Context) {
		_, _, ok := activeAPIKeyViewerContext(c)
		if !ok {
			return
		}
		if identities == nil || quotas == nil {
			writeInternalError(c, "Claude quota provider is not configured", nil)
			return
		}
		authIndexes, err := identities.ListActiveClaudeAuthIndexes(c.Request.Context())
		if err != nil {
			writeInternalError(c, "Claude quota identity lookup failed", err)
			return
		}
		response := keyClaudeQuotaResponse{Items: []keyClaudeQuotaItem{}}
		if len(authIndexes) == 0 {
			c.JSON(http.StatusOK, response)
			return
		}
		cached, err := quotas.GetCachedQuota(c.Request.Context(), quota.CacheRequest{AuthIndexes: authIndexes})
		if err != nil {
			writeInternalError(c, "Claude quota cache lookup failed", err)
			return
		}
		response.Items = make([]keyClaudeQuotaItem, 0, len(cached.Items))
		for _, item := range cached.Items {
			viewerItem := keyClaudeQuotaItem{
				Status: item.Status, RefreshedAt: item.RefreshedAt, ExpiresAt: item.ExpiresAt,
			}
			if item.Quota != nil {
				for _, row := range item.Quota.Quota {
					if row.Key != "five_hour" && row.Key != "seven_day" && row.Key != "seven_day_fable" {
						continue
					}
					viewerItem.Quota = append(viewerItem.Quota, keyClaudeQuotaRow{
						Key: row.Key, Label: row.Label, UsedPercent: row.UsedPercent, ResetAt: row.ResetAt,
					})
				}
				if item.Quota.Subscription != nil {
					viewerItem.Subscription = &keyClaudeSubscriptionInfo{Plan: item.Quota.Subscription.Plan}
				}
			}
			response.Items = append(response.Items, viewerItem)
		}
		c.JSON(http.StatusOK, response)
	})
}
