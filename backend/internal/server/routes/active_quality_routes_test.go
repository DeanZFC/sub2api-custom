package routes

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/Wei-Shaw/sub2api/internal/handler/admin"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestActiveQualityRoutesRetirePassiveProtection(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	handlers := &handler.Handlers{Admin: &handler.AdminHandlers{
		ScheduledTest:  admin.NewScheduledTestHandler(nil),
		ChannelQuality: admin.NewChannelQualityHandler(nil),
	}}
	registerScheduledTestRoutes(router.Group("/admin"), handlers)
	routes := map[string]bool{}
	for _, route := range router.Routes() {
		routes[route.Method+" "+route.Path] = true
	}
	for _, route := range []string{
		"GET /admin/test-plans", "POST /admin/test-plans", "GET /admin/scheduled-test-definitions",
		"POST /admin/test-results/:id/decision", "GET /admin/accounts/:id/quality-protection",
		"GET /admin/channel-quality/operations", "PUT /admin/channel-quality/operations",
	} {
		require.True(t, routes[route], route)
	}
	for _, route := range []string{
		"GET /admin/passive-quality-policies", "POST /admin/passive-quality-policies", "GET /admin/passive-quality-states",
		"GET /admin/passive-quality-events", "GET /admin/passive-quality/group-targets", "GET /admin/passive-quality/runtime",
		"GET /admin/channel-quality", "POST /admin/channel-quality/suppliers",
	} {
		require.False(t, routes[route], route)
	}
}
