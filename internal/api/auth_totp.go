package api

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"strings"

	"cpa-usage-keeper/internal/auth"
	"github.com/gin-gonic/gin"
)

type totpStatusResponse struct {
	Enabled bool `json:"enabled"`
	Pending bool `json:"pending"`
}

type totpSetupResponse struct {
	OTPAuthURI string `json:"otpauth_uri"`
	Secret     string `json:"secret"`
}

type totpCodeRequest struct {
	Code string `json:"code"`
}

type totpDisableRequest struct {
	Password string `json:"password"`
	Code     string `json:"code"`
}

func registerTOTPManagementRoutes(router gin.IRoutes, handler *authHandler) {
	router.GET("/auth/totp", handler.getTOTPStatus)
	router.POST("/auth/totp/setup", handler.setupTOTP)
	router.POST("/auth/totp/confirm", handler.confirmTOTP)
	router.POST("/auth/totp/disable", handler.disableTOTP)
}

func (h *authHandler) getTOTPStatus(c *gin.Context) {
	if h == nil || !h.config.Enabled || h.totp == nil {
		c.JSON(http.StatusOK, totpStatusResponse{})
		return
	}
	ctx := c.Request.Context()
	c.JSON(http.StatusOK, totpStatusResponse{Enabled: h.totp.Enrolled(ctx), Pending: h.totp.HasPending(ctx)})
}

func (h *authHandler) setupTOTP(c *gin.Context) {
	if h == nil || !h.config.Enabled || h.totp == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "totp is not configured"})
		return
	}
	// 遗忘的 AUTH_TOTP_RESET 会在重启时清空注册，这里直接拒绝新注册避免惊喜。
	if h.config.TOTPReset {
		c.JSON(http.StatusConflict, gin.H{"error": "totp reset is active"})
		return
	}
	uri, secret, err := h.totp.CreatePending(c.Request.Context())
	if err != nil {
		writeInternalError(c, "create totp enrollment failed", err)
		return
	}
	c.JSON(http.StatusOK, totpSetupResponse{OTPAuthURI: uri, Secret: secret})
}

func (h *authHandler) confirmTOTP(c *gin.Context) {
	if h == nil || !h.config.Enabled || h.totp == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "totp is not configured"})
		return
	}
	if !h.allowLoginAttempt(c, totpConfirmAttemptKey(c)) {
		return
	}
	var request totpCodeRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	confirmed, err := h.totp.ConfirmPending(c.Request.Context(), strings.TrimSpace(request.Code))
	if err != nil {
		switch {
		case errors.Is(err, auth.ErrTOTPNoPending):
			c.JSON(http.StatusBadRequest, gin.H{"error": "no pending enrollment"})
		case errors.Is(err, auth.ErrTOTPPendingExpired):
			c.JSON(http.StatusBadRequest, gin.H{"error": "pending enrollment expired"})
		default:
			writeInternalError(c, "confirm totp enrollment failed", err)
		}
		return
	}
	if !confirmed {
		c.JSON(http.StatusUnauthorized, gin.H{"error": invalidTOTPCodeError})
		return
	}
	h.loginAttempts.Reset(totpConfirmAttemptKey(c))
	c.Status(http.StatusNoContent)
}

func (h *authHandler) disableTOTP(c *gin.Context) {
	if h == nil || !h.config.Enabled || h.totp == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "totp is not configured"})
		return
	}
	var request totpDisableRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	if subtle.ConstantTimeCompare([]byte(request.Password), []byte(h.config.LoginPassword)) != 1 {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	valid, err := h.totp.Verify(c.Request.Context(), strings.TrimSpace(request.Code))
	if err != nil {
		writeInternalError(c, "verify totp code failed", err)
		return
	}
	if !valid {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "invalid credentials"})
		return
	}
	if err := h.totp.Disable(c.Request.Context()); err != nil {
		writeInternalError(c, "disable totp enrollment failed", err)
		return
	}
	c.Status(http.StatusNoContent)
}

func totpConfirmAttemptKey(c *gin.Context) string {
	return "totp-confirm:" + loginClientKey(c)
}
