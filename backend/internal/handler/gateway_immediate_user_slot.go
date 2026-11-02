package handler

import (
	"net/http"

	middleware "github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// Voice and standalone search must acquire the same user/group dimensions as
// text requests, without sending SSE while a WebSocket handshake is pending.
func acquireImmediateGatewayUserSlot(c *gin.Context, helper *ConcurrencyHelper, key *service.APIKey) (func(), bool) {
	if helper == nil || key == nil || key.User == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "User concurrency unavailable"}})
		return nil, false
	}
	userID, userMax := key.User.ID, key.User.Concurrency
	if subject, ok := middleware.GetAuthSubjectFromContext(c); ok {
		userID, userMax = subject.UserID, subject.Concurrency
	}
	groupID, groupMax := apiKeyUserConcurrencyLimit(key)
	release, acquired, err := helper.TryAcquireUserSlotForAPIKeyWithGroup(c.Request.Context(), userID, userMax, key.ID, groupID, groupMax)
	if err != nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": gin.H{"type": "api_error", "message": "User concurrency unavailable"}})
		return nil, false
	}
	if !acquired {
		c.JSON(http.StatusTooManyRequests, gin.H{"error": gin.H{"type": "rate_limit_error", "message": "User or group concurrency limit reached"}})
		return nil, false
	}
	return wrapReleaseOnDone(c.Request.Context(), release), true
}
