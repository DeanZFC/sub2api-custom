package handler

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UserHandler) ListTestVotes(c *gin.Context) {
	_, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	// Keep older clients compatible while public quality voting is disabled.
	c.JSON(http.StatusOK, []*service.ScheduledTestVoteResult{})
}

func (h *UserHandler) VoteTestResult(c *gin.Context) {
	_, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	// Enforce this at the API boundary too: a hidden button cannot prevent a
	// direct request from manipulating quality-based account routing.
	response.Forbidden(c, "user test voting is disabled")
}
