//go:build unit

package middleware

import (
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSharedPoolGatewayAdmission(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name                          string
		shared, enabled, subscription bool
		want                          int
	}{
		{"ordinary group unaffected", false, false, false, 200},
		{"shared disabled", true, false, false, 403},
		{"shared enabled", true, true, false, 200},
		{"shared subscription rejected", true, true, true, 403},
	} {
		t.Run(tc.name, func(t *testing.T) {
			enabled := "false"
			if tc.enabled {
				enabled = "true"
			}
			settings := service.NewSettingService(fakeSettingRepo{values: map[string]string{service.SettingKeySharedPoolEnabled: enabled}}, &config.Config{})
			id := int64(1)
			group := &service.Group{ID: id, IsSharedPool: tc.shared, SubscriptionType: "standard"}
			if tc.subscription {
				group.SubscriptionType = "subscription"
			}
			r := gin.New()
			r.Use(func(c *gin.Context) {
				c.Set(string(ContextKeyAPIKey), &service.APIKey{GroupID: &id, Group: group})
				c.Next()
			})
			r.Use(RequireGroupAssignment(settings, AnthropicErrorWriter))
			r.GET("/t", func(c *gin.Context) { c.Status(http.StatusOK) })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/t", nil))
			require.Equal(t, tc.want, w.Code)
		})
	}
}
