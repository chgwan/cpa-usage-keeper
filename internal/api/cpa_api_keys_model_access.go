package api

import (
	"errors"
	"net/http"
	"strconv"

	"cpa-usage-keeper/internal/service"

	"github.com/gin-gonic/gin"
	"github.com/sirupsen/logrus"
	"gorm.io/gorm"
)

type keyModelAccessStatusResponse struct {
	Version               string `json:"version"`
	PolicyCount           int    `json:"policyCount"`
	Revision              uint64 `json:"revision"`
	PolicyFile            string `json:"policyFile"`
	PersistentUpdates     bool   `json:"persistentUpdates"`
	Source                string `json:"source"`
	UpdatedAt             string `json:"updatedAt"`
	FailClosed            bool   `json:"failClosed"`
	LastError             string `json:"lastError"`
	PersistenceSetupError string `json:"persistenceSetupError,omitempty"`
}

type keyModelAccessRuleResponse struct {
	AllowModels []string `json:"allowModels"`
	DenyModels  []string `json:"denyModels"`
}

type keyModelAccessStateResponse struct {
	Available   bool                                  `json:"available"`
	Status      *keyModelAccessStatusResponse         `json:"status,omitempty"`
	Models      []string                              `json:"models"`
	ModelsError string                                `json:"modelsError,omitempty"`
	Policies    map[string]keyModelAccessRuleResponse `json:"policies"`
}

type updateKeyModelAccessRequest struct {
	APIKeyIDs   []string `json:"apiKeyIds"`
	AllowModels []string `json:"allowModels"`
	DenyModels  []string `json:"denyModels"`
}

func toKeyModelAccessStateResponse(state service.KeyModelAccessState) keyModelAccessStateResponse {
	models := state.Models
	if models == nil {
		models = []string{}
	}
	policies := make(map[string]keyModelAccessRuleResponse, len(state.Policies))
	for id, rule := range state.Policies {
		allow := rule.AllowModels
		if allow == nil {
			allow = []string{}
		}
		deny := rule.DenyModels
		if deny == nil {
			deny = []string{}
		}
		policies[strconv.FormatInt(id, 10)] = keyModelAccessRuleResponse{
			AllowModels: allow,
			DenyModels:  deny,
		}
	}
	var statusResp *keyModelAccessStatusResponse
	if state.Status != nil {
		statusResp = &keyModelAccessStatusResponse{
			Version:               state.Status.Version,
			PolicyCount:           state.Status.PolicyCount,
			Revision:              state.Status.Revision,
			PolicyFile:            state.Status.PolicyFile,
			PersistentUpdates:     state.Status.PersistentUpdates,
			Source:                state.Status.Source,
			UpdatedAt:             state.Status.UpdatedAt,
			FailClosed:            state.Status.FailClosed,
			LastError:             state.Status.LastError,
			PersistenceSetupError: state.Status.PersistenceSetupError,
		}
	}
	return keyModelAccessStateResponse{
		Available:   state.Available,
		Status:      statusResp,
		Models:      models,
		ModelsError: state.ModelsError,
		Policies:    policies,
	}
}

func writeKeyModelAccessError(c *gin.Context, message string, err error) {
	switch {
	case errors.Is(err, service.ErrKeyModelAccessUnavailable):
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "key-model-access plugin is unavailable", "code": "plugin_unavailable"})
	case errors.Is(err, service.ErrKeyModelAccessConflict):
		c.JSON(http.StatusConflict, gin.H{"error": "model access policies changed since last load; refresh and try again", "code": "policy_conflict"})
	case errors.Is(err, service.ErrKeyModelAccessNotPersistent):
		c.JSON(http.StatusConflict, gin.H{"error": "policy_file is not configured; cannot reload from file", "code": "not_persistent"})
	case errors.Is(err, service.ErrCPARequestFailed):
		c.JSON(http.StatusBadGateway, gin.H{"error": "cpa api request failed"})
	case errors.Is(err, service.ErrInvalidID):
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
	case errors.Is(err, gorm.ErrRecordNotFound):
		c.JSON(http.StatusNotFound, gin.H{"error": "api key not found"})
	case errors.Is(err, service.ErrInvalidInput):
		c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
	default:
		logrus.WithError(err).Error(message)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "internal server error"})
	}
}

func registerCPAAPIKeyModelAccessRoutes(router gin.IRoutes, provider service.KeyModelAccessProvider) {
	router.GET("/usage/api-keys/model-access", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if provider == nil {
			c.JSON(http.StatusOK, toKeyModelAccessStateResponse(service.KeyModelAccessState{Available: false}))
			return
		}
		state, err := provider.GetState(c.Request.Context())
		if err != nil {
			writeKeyModelAccessError(c, "load api key model access state failed", err)
			return
		}
		c.JSON(http.StatusOK, toKeyModelAccessStateResponse(state))
	})

	router.PUT("/usage/api-keys/model-access", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "key-model-access provider is not configured"})
			return
		}
		var request updateKeyModelAccessRequest
		if err := c.ShouldBindJSON(&request); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
			return
		}
		ids, ok := parseCPAAPIKeyIDList(request.APIKeyIDs)
		if !ok {
			c.JSON(http.StatusBadRequest, gin.H{"error": "invalid api key id"})
			return
		}
		state, err := provider.UpdateRules(c.Request.Context(), ids, request.AllowModels, request.DenyModels)
		if err != nil {
			writeKeyModelAccessError(c, "update api key model access rules failed", err)
			return
		}
		c.JSON(http.StatusOK, toKeyModelAccessStateResponse(state))
	})

	router.POST("/usage/api-keys/model-access/reload", func(c *gin.Context) {
		setNoStoreHeaders(c)
		if provider == nil {
			c.JSON(http.StatusNotImplemented, gin.H{"error": "key-model-access provider is not configured"})
			return
		}
		state, err := provider.Reload(c.Request.Context())
		if err != nil {
			writeKeyModelAccessError(c, "reload api key model access policies failed", err)
			return
		}
		c.JSON(http.StatusOK, toKeyModelAccessStateResponse(state))
	})
}
