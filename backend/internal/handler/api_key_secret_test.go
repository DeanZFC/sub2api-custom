//go:build unit

package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAPIKeySecretIdempotencyNeverStoresOrReplaysSecret(t *testing.T) {
	gin.SetMode(gin.TestMode)
	repo := newUserMemoryIdempotencyRepoStub()
	service.SetDefaultIdempotencyCoordinator(service.NewIdempotencyCoordinator(repo, service.DefaultIdempotencyConfig()))
	t.Cleanup(func() { service.SetDefaultIdempotencyCoordinator(nil) })
	const raw = "sk-first-response-only-123456789"
	calls := 0
	router := gin.New()
	router.Use(withUserSubject(3))
	router.POST("/secret", func(c *gin.Context) {
		apiKeyNoStore(c)
		executeUserIdempotentJSON(c, "user.api_keys.regenerate", gin.H{"id": 7}, time.Minute, func(context.Context) (any, error) {
			calls++
			return &dto.APIKeySecret{APIKey: *dto.APIKeyFromService(&service.APIKey{ID: 7, Key: raw}), Key: raw}, nil
		})
	})
	for i := 0; i < 2; i++ {
		req := httptest.NewRequest(http.MethodPost, "/secret", nil)
		req.Header.Set("Idempotency-Key", "repeat-safe")
		rec := httptest.NewRecorder()
		router.ServeHTTP(rec, req)
		require.Equal(t, 200, rec.Code)
		require.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
		if i == 0 {
			require.Contains(t, rec.Body.String(), raw)
		} else {
			require.NotContains(t, rec.Body.String(), raw)
			require.Equal(t, "true", rec.Header().Get("X-Idempotency-Replayed"))
		}
	}
	require.Equal(t, 1, calls)
	for _, record := range repo.data {
		require.NotContains(t, *record.ResponseBody, raw)
	}
}

type keyStepUpUsers struct {
	service.UserRepository
	enabled bool
	err     error
}

func (r *keyStepUpUsers) GetByID(context.Context, int64) (*service.User, error) {
	return &service.User{ID: 3, TotpEnabled: r.enabled}, r.err
}

type keyStepUpCache struct {
	service.TotpCache
	granted bool
	err     error
}

func (r *keyStepUpCache) HasStepUpGrant(context.Context, int64, string) (bool, error) {
	return r.granted, r.err
}

func TestAPIKeySecretStepUpOnlyForEnabledUsersAndFailsClosed(t *testing.T) {
	for _, tc := range []struct {
		name                   string
		enabled, granted, fail bool
		want                   int
	}{
		{"no TOTP", false, false, false, 200},
		{"TOTP required", true, false, false, 403},
		{"TOTP verified", true, true, false, 200},
		{"cache unavailable", true, false, true, 503},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cache := &keyStepUpCache{granted: tc.granted}
			if tc.fail {
				cache.err = service.ErrAPIKeyNotFound
			}
			totp := service.NewTotpService(&keyStepUpUsers{enabled: tc.enabled}, nil, cache, nil, nil, nil)
			h := NewTotpHandler(totp)
			r := gin.New()
			r.Use(withUserSubject(3))
			r.POST("/keys", h.RequireKeyStepUp, func(c *gin.Context) { c.Status(200) })
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/keys", nil))
			require.Equal(t, tc.want, rec.Code, rec.Body.String())
		})
	}
}
