package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Account429RetryScope gives every downstream HTTP request one shared,
// per-account 429 retry budget. All upstream attempts and official failover
// loops derived from the request therefore consume the same configured limit.
func Account429RetryScope() gin.HandlerFunc {
	return func(c *gin.Context) {
		if c != nil && c.Request != nil {
			c.Request = c.Request.WithContext(service.WithAccount429RetryScope(c.Request.Context()))
		}
		c.Next()
	}
}
