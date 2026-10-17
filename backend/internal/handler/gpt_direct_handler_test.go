package handler

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGPTDirectHandlerRequiresAuthAndRejectsInjectedScope(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		auth   bool
		body   string
		status int
	}{
		{false, `{"code":"CODE-123"}`, 401},
		{true, `{"code":"CODE-123","tenantSlug":"another"}`, 400},
		{true, `{"code":"CODE-123","product":"claude"}`, 400},
		{true, `{"session":"` + strings.Repeat("a", 100*1024) + `"}`, 400},
		{true, `{} {}`, 400},
	} {
		router := gin.New()
		if tc.auth {
			router.Use(func(c *gin.Context) { c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: 1}) })
		}
		handler := &GPTDirectHandler{}
		router.POST("/verify", handler.Operation("verify"))
		recorder := httptest.NewRecorder()
		request := httptest.NewRequest(http.MethodPost, "/verify", strings.NewReader(tc.body))
		request.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, request)
		require.Equal(t, tc.status, recorder.Code)
		require.NotContains(t, recorder.Body.String(), "another")
	}
}
