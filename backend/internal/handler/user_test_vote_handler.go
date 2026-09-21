package handler

import (
	"database/sql"
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
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	if h.scheduledTestSvc == nil {
		response.InternalError(c, "test voting unavailable")
		return
	}
	rows, err := h.scheduledTestSvc.ListVotingResults(c.Request.Context(), subject.UserID)
	if err != nil {
		response.InternalError(c, "test voting unavailable")
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *UserHandler) VoteTestResult(c *gin.Context) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok {
		response.Unauthorized(c, "authentication required")
		return
	}
	if h.scheduledTestSvc == nil {
		response.InternalError(c, "test voting unavailable")
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid test result id")
		return
	}
	var req struct {
		Vote string `json:"vote"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "vote must be pass or fail")
		return
	}
	row, err := h.scheduledTestSvc.CastTestVote(c.Request.Context(), subject.UserID, id, req.Vote)
	switch {
	case errors.Is(err, service.ErrScheduledTestVoteInvalid):
		response.BadRequest(c, service.ErrScheduledTestVoteInvalid.Error())
	case errors.Is(err, service.ErrScheduledTestVoteUnavailable), errors.Is(err, sql.ErrNoRows):
		response.NotFound(c, "this voting round is unavailable; refresh the page")
	case err != nil:
		response.InternalError(c, "unable to save vote")
	default:
		c.JSON(http.StatusOK, row)
	}
}
