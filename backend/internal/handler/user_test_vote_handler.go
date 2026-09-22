package handler

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *UserHandler) ListTestVotes(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return
	}
	if h.scheduledTestSvc == nil {
		response.Error(c, http.StatusServiceUnavailable, "test voting unavailable")
		return
	}
	rows, err := h.scheduledTestSvc.ListVotingResults(c.Request.Context(), subject.UserID)
	if err != nil {
		if errors.Is(err, service.ErrScheduledTestVoteUnavailable) {
			response.Error(c, http.StatusServiceUnavailable, "test voting unavailable")
		} else {
			response.InternalError(c, "failed to load test votes")
		}
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *UserHandler) VoteTestResult(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return
	}
	if h.scheduledTestSvc == nil {
		response.Error(c, http.StatusServiceUnavailable, "test voting unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid test result id")
		return
	}
	var req struct {
		Vote string `json:"vote" binding:"required,oneof=pass fail"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "vote must be pass or fail")
		return
	}
	row, err := h.scheduledTestSvc.CastTestVote(c.Request.Context(), subject.UserID, id, req.Vote)
	if err != nil {
		switch {
		case errors.Is(err, service.ErrScheduledTestVoteUnavailable):
			response.Error(c, http.StatusConflict, "this voting round changed or is unavailable; refresh before voting")
		case errors.Is(err, service.ErrScheduledTestVoteInvalid):
			response.BadRequest(c, "vote must be pass or fail")
		default:
			response.InternalError(c, "failed to save test vote")
		}
		return
	}
	c.JSON(http.StatusOK, row)
}
