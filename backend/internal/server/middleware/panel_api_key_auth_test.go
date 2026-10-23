//go:build unit

package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type panelKeyRepo struct {
	service.APIKeyRepository
	key *service.APIKey
}

func (r *panelKeyRepo) GetByIDForAuth(context.Context, int64) (*service.APIKey, error) {
	clone := *r.key
	return &clone, nil
}
func (r *panelKeyRepo) UpdateLastUsed(context.Context, int64, time.Time) error { return nil }

func TestPanelAPIKeyAuthOwnershipAndGatewayPolicies(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tc := range []struct {
		name, method, status           string
		userID                         int64
		ipBlocked, groupBlocked, quota bool
		want                           int
	}{
		{"owner", http.MethodPost, service.StatusActive, 3, false, false, false, 200},
		{"unauthenticated", http.MethodPost, service.StatusActive, 0, false, false, false, 401},
		{"other owner", http.MethodPost, service.StatusActive, 4, false, false, false, 404},
		{"disabled", http.MethodPost, "inactive", 3, false, false, false, 403},
		{"IP ACL", http.MethodPost, service.StatusActive, 3, true, false, false, 403},
		{"group disabled", http.MethodPost, service.StatusActive, 3, false, true, false, 403},
		{"quota rejects submit", http.MethodPost, service.StatusActive, 3, false, false, true, 429},
		{"quota permits existing results", http.MethodGet, service.StatusActive, 3, false, false, true, 200},
	} {
		t.Run(tc.name, func(t *testing.T) {
			group := &service.Group{ID: 5, Status: service.StatusActive, Hydrated: true, Platform: service.PlatformGemini}
			if tc.groupBlocked {
				group.Status = "inactive"
			}
			key := &service.APIKey{ID: 7, UserID: 3, Status: tc.status, User: &service.User{ID: 3, Status: service.StatusActive, Balance: 100}, GroupID: &group.ID, Group: group}
			if tc.quota {
				key.Quota = 1
				key.QuotaUsed = 1
			}
			if tc.ipBlocked {
				key.IPWhitelist = []string{"192.0.2.1"}
			}
			cfg := &config.Config{RunMode: config.RunModeStandard}
			svc := service.NewAPIKeyService(&panelKeyRepo{key: key}, nil, nil, nil, nil, nil, cfg)
			r := gin.New()
			if tc.userID > 0 {
				r.Use(func(c *gin.Context) { c.Set(string(ContextKeyUser), AuthSubject{UserID: tc.userID}); c.Next() })
			}
			r.Handle(tc.method, "/api/v1/key-image-batches/:key_id", PanelAPIKeyAuth(svc, nil, cfg), func(c *gin.Context) {
				got, ok := GetAPIKeyFromContext(c)
				require.True(t, ok)
				require.Equal(t, int64(7), got.ID)
				require.Empty(t, got.Key)
				c.Status(200)
			})
			req := httptest.NewRequest(tc.method, "/api/v1/key-image-batches/7", nil)
			req.RemoteAddr = "127.0.0.1:1234"
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, req)
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}
