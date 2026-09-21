package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func testReviewAdmin(c *gin.Context) (int64, bool) {
	subject, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || subject.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return 0, false
	}
	role, _ := middleware.GetUserRoleFromContext(c)
	if role != service.RoleAdmin {
		response.Forbidden(c, "administrator access required")
		return 0, false
	}
	return subject.UserID, true
}

func (h *ScheduledTestHandler) ListAdminReviews(c *gin.Context) {
	if _, ok := testReviewAdmin(c); !ok {
		return
	}
	rows, err := h.scheduledTestSvc.ListAdminReviews(c.Request.Context())
	if err != nil {
		response.InternalError(c, "failed to load quality reviews")
		return
	}
	c.JSON(http.StatusOK, rows)
}

func (h *ScheduledTestHandler) DecideResult(c *gin.Context) {
	adminID, ok := testReviewAdmin(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid result id")
		return
	}
	var req struct {
		Generation int64  `json:"generation" binding:"required,gt=0"`
		Verdict    string `json:"verdict" binding:"required,oneof=pass fail"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "generation and pass/fail verdict are required")
		return
	}
	if err := h.scheduledTestSvc.DecideTestResult(c.Request.Context(), adminID, id, req.Generation, req.Verdict); err != nil {
		if errors.Is(err, service.ErrScheduledTestVoteUnavailable) {
			response.Error(c, http.StatusConflict, "this quality review changed or is unavailable; refresh before deciding")
		} else {
			response.InternalError(c, "failed to save quality decision")
		}
		return
	}
	c.JSON(http.StatusOK, gin.H{"message": "decision saved"})
}
