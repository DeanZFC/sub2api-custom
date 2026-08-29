package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAccount429RetryScopeAttachesOneIdempotentRequestScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(Account429RetryScope())
	router.GET("/scope", func(c *gin.Context) {
		requestCtx := c.Request.Context()
		require.Same(t, requestCtx, service.WithAccount429RetryScope(requestCtx))
		c.Status(http.StatusNoContent)
	})

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/scope", nil))
	require.Equal(t, http.StatusNoContent, recorder.Code)
}
