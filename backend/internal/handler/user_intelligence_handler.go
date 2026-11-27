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
	"github.com/google/uuid"
)

type intelligenceQuery struct {
	repo            service.PublicIntelligenceRepository
	userID, groupID int64
	model           string
	page            int
}

func (h *UserHandler) intelligenceQuery(c *gin.Context) (*intelligenceQuery, bool) {
	u, ok := middleware.GetAuthSubjectFromContext(c)
	if !ok || u.UserID <= 0 {
		response.Unauthorized(c, "authentication required")
		return nil, false
	}
	q := &intelligenceQuery{userID: u.UserID, model: c.Query("model"), page: 1}
	var err error
	if value := c.Param("group"); value != "" {
		q.groupID, err = strconv.ParseInt(value, 10, 64)
		if err != nil || q.groupID <= 0 {
			response.BadRequest(c, "invalid group")
			return nil, false
		}
	}
	if len(q.model) > 100 {
		response.BadRequest(c, "invalid model")
		return nil, false
	}
	if value, exists := c.GetQuery("page"); exists {
		p, err := strconv.ParseInt(value, 10, 32)
		if err != nil || p < 1 {
			response.BadRequest(c, "invalid page")
			return nil, false
		}
		q.page = int(p)
	}
	q.repo, err = h.scheduledTestSvc.PublicIntelligence()
	if err != nil {
		response.Error(c, http.StatusServiceUnavailable, "intelligence monitoring unavailable")
		return nil, false
	}
	c.Header("Cache-Control", "private, no-store")
	return q, true
}
func intelligenceResponse(c *gin.Context, payload any, err error) {
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "inspection unavailable")
		return
	}
	if err != nil {
		response.InternalError(c, "failed to load inspection")
		return
	}
	c.JSON(http.StatusOK, payload)
}
func (h *UserHandler) IntelligenceGroups(c *gin.Context) {
	h.intelligenceDirectory(c, true)
}

func (h *UserHandler) IntelligenceDirectory(c *gin.Context) {
	h.intelligenceDirectory(c, false)
}

func (h *UserHandler) intelligenceDirectory(c *gin.Context, groupsOnly bool) {
	q, ok := h.intelligenceQuery(c)
	if !ok {
		return
	}
	directory, err := q.repo.PublicIntelligenceDirectory(c.Request.Context(), q.userID)
	if groupsOnly && err == nil {
		// Keep the list response for existing clients.
		intelligenceResponse(c, directory.Groups, nil)
		return
	}
	intelligenceResponse(c, directory, err)
}
func (h *UserHandler) IntelligenceRounds(c *gin.Context) {
	q, ok := h.intelligenceQuery(c)
	if !ok {
		return
	}
	v, err := q.repo.PublicIntelligenceRounds(c.Request.Context(), q.userID, q.groupID, q.model)
	intelligenceResponse(c, gin.H{"rounds": v}, err)
}
func (h *UserHandler) IntelligenceRoundDetail(c *gin.Context) {
	q, ok := h.intelligenceQuery(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("run"))
	if err != nil {
		response.BadRequest(c, "invalid run")
		return
	}
	v, err := q.repo.PublicIntelligenceRoundDetails(c.Request.Context(), q.userID, q.groupID, q.model, id.String(), q.page)
	intelligenceResponse(c, v, err)
}
func (h *UserHandler) IntelligenceSummary(c *gin.Context) {
	q, ok := h.intelligenceQuery(c)
	if !ok {
		return
	}
	v, err := q.repo.PublicIntelligenceSummary(c.Request.Context(), q.userID, q.groupID, q.model, q.page)
	intelligenceResponse(c, v, err)
}
func (h *UserHandler) IntelligenceArtwork(c *gin.Context) {
	q, ok := h.intelligenceQuery(c)
	if !ok {
		return
	}
	id, err := strconv.ParseInt(c.Param("result"), 10, 64)
	if err != nil || id < 1 {
		response.BadRequest(c, "invalid result")
		return
	}
	v, err := q.repo.PublicIntelligenceArtwork(c.Request.Context(), q.userID, q.groupID, id)
	intelligenceResponse(c, gin.H{"output_html": v}, err)
}
