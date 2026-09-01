package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Account429RetryScope keeps the old request context hook source-compatible.
// Account-level transparent 429 retries are disabled, so the attached budget
// is no longer consumed by upstream requests.
func Account429RetryScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c != nil && c.Request != nil {
			c.Request = c.Request.WithContext(service.WithAccount429RetryScope(c.Request.Context()))
		}
		c.Next()
	}
}
