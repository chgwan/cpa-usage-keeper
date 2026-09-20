package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
)

const (
	unauthenticatedLoginBodyLimit   int64 = 4 << 10
	unauthenticatedLoginReadTimeout       = 15 * time.Second
	// 所有路由（含匿名入口与 404 等拒绝路径）共享的 body 上限，最大的合法请求体仍远小于此值。
	generalRequestBodyLimit int64 = 1 << 20
)

// requestBodyLimits 给每个请求的 body 加硬上限，未声明长度的分块请求在超出后由 MaxBytesReader 中断。
func requestBodyLimits() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request.Body == nil || c.Request.Body == http.NoBody {
			c.Next()
			return
		}
		if c.Request.ContentLength > generalRequestBodyLimit {
			_ = c.Request.Body.Close()
			writeRequestEntityTooLarge(c)
			c.Abort()
			return
		}
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, generalRequestBodyLimit)
		c.Next()
	}
}

func unauthenticatedLoginRequestLimits(basePath string) gin.HandlerFunc {
	prefix := strings.TrimSuffix(basePath, "/") + "/api/v1/auth/"
	loginPath := prefix + "login"
	apiKeyLoginPath := prefix + "api-key-login"
	return func(c *gin.Context) {
		if c.Request.Method != http.MethodPost || (c.Request.URL.Path != loginPath && c.Request.URL.Path != apiKeyLoginPath) {
			c.Next()
			return
		}

		// 匿名登录入口先建立读取期限，保证后续校验提前返回或关闭 body 时仍有时间上限。
		controller := http.NewResponseController(c.Writer)
		deadlineSet := controller.SetReadDeadline(time.Now().Add(unauthenticatedLoginReadTimeout)) == nil
		defer func() {
			if c.Request.Body != nil {
				_ = c.Request.Body.Close()
			}
			if deadlineSet {
				_ = controller.SetReadDeadline(time.Time{})
			}
		}()

		if c.Request.ContentLength > unauthenticatedLoginBodyLimit {
			writeRequestEntityTooLarge(c)
			c.Abort()
			return
		}
		if c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, unauthenticatedLoginBodyLimit)
		}

		c.Next()
	}
}

func isRequestEntityTooLarge(err error) bool {
	var maxBytesError *http.MaxBytesError
	return errors.As(err, &maxBytesError)
}

func writeRequestEntityTooLarge(c *gin.Context) {
	c.JSON(http.StatusRequestEntityTooLarge, gin.H{"error": "request body too large"})
}
