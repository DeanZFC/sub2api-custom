package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func UpstreamErrorRetry(settings *service.SettingService) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Request = c.Request.WithContext(service.WithUpstreamErrorRetry(c.Request.Context(), settings))
		c.Next()
	}
}
