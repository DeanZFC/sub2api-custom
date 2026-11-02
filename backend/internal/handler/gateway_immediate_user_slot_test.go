package handler

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestImmediateGatewayUserSlotEnforcesGroupAndReleases(t *testing.T) {
	for _, allowed := range []bool{true, false} {
		cache := &helperConcurrencyCacheStub{userGroupSeq: []bool{allowed}}
		helper := NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Millisecond)
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		c.Request = httptest.NewRequest("POST", "/v1/audio/speech", nil)
		key := &service.APIKey{ID: 1, User: &service.User{ID: 7, Concurrency: 10}, Group: &service.Group{ID: 20, UserConcurrencyLimit: 1}}
		release, ok := acquireImmediateGatewayUserSlot(c, helper, key)
		require.Equal(t, allowed, ok)
		require.EqualValues(t, 20, cache.lastUserGroupID)
		require.Equal(t, 1, cache.lastUserGroupMax)
		if !allowed {
			require.Equal(t, http.StatusTooManyRequests, rec.Code)
			continue
		}
		require.False(t, c.Writer.Written(), "slot admission must not commit a WebSocket handshake response")
		release()
		release()
		require.Equal(t, 1, cache.userGroupReleaseCalls)
		require.Equal(t, 1, cache.apiKeyReleaseCalls)
	}
}
