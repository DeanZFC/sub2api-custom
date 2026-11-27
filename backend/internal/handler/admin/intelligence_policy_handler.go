package admin

import (
	"database/sql"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func intelligenceID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid id")
		return 0, false
	}
	return id, true
}

func (h *SettingHandler) GetIntelligenceVisibility(c *gin.Context) {
	enabled, err := h.settingService.IntelligenceRoundsVisible(c.Request.Context())
	if err != nil {
		response.InternalError(c, "unable to read intelligence visibility")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.Success(c, gin.H{"enabled": enabled})
}

func (h *SettingHandler) UpdateIntelligenceVisibility(c *gin.Context) {
	var req struct {
		Enabled *bool `json:"enabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "enabled must be a boolean")
		return
	}
	if err := h.settingService.SetIntelligenceRoundsVisible(c.Request.Context(), *req.Enabled); err != nil {
		response.InternalError(c, "unable to save intelligence visibility")
		return
	}
	response.Success(c, gin.H{"enabled": *req.Enabled})
}

func (h *ScheduledTestHandler) IntelligenceOverview(c *gin.Context) {
	id, ok := intelligenceID(c)
	if !ok {
		return
	}
	v, err := h.scheduledTestSvc.IntelligenceOverview(c.Request.Context(), id)
	if err != nil {
		response.InternalError(c, "unable to load intelligence results")
		return
	}
	c.JSON(http.StatusOK, v)
}
func (h *ScheduledTestHandler) IntelligenceArtwork(c *gin.Context) {
	id, ok := intelligenceID(c)
	if !ok {
		return
	}
	html, err := h.scheduledTestSvc.IntelligenceArtwork(c.Request.Context(), id)
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "artwork unavailable")
		return
	}
	if err != nil {
		response.InternalError(c, "unable to load artwork")
		return
	}
	c.JSON(http.StatusOK, gin.H{"html": html})
}
func (h *ScheduledTestHandler) ReviewIntelligenceArt(c *gin.Context) {
	admin, ok := testReviewAdmin(c)
	if !ok {
		return
	}
	id, ok := intelligenceID(c)
	if !ok {
		return
	}
	var req struct {
		Verdict string `json:"verdict" binding:"required,oneof=pass fail"`
		Reason  string `json:"reason" binding:"max=500"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid artwork review")
		return
	}
	err := h.scheduledTestSvc.ReviewIntelligenceArt(c.Request.Context(), id, admin, req.Verdict, strings.TrimSpace(req.Reason))
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "artwork unavailable")
		return
	}
	if err != nil {
		response.InternalError(c, "unable to save review")
		return
	}
	c.JSON(http.StatusOK, gin.H{"verdict": req.Verdict})
}

// Count the same account/model pairs as the execution snapshot. The response
// contains only IDs and counts; account configuration stays with the service.
func (h *GroupHandler) IntelligenceScope(c *gin.Context) {
	var req struct {
		Groups   []int64  `json:"group_ids" binding:"required,min=1,max=100,dive,gt=0"`
		Models   []string `json:"model_ids" binding:"max=16,dive,min=1,max=200"`
		Excluded []int64  `json:"excluded_account_ids" binding:"max=10000,dive,gt=0"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid detection scope")
		return
	}
	excluded := make(map[int64]bool, len(req.Excluded))
	for _, id := range req.Excluded {
		excluded[id] = true
	}
	seen := map[int64]int{}
	counts := map[int64]int{}
	now := time.Now()
	ids := []int64{}
	total := 0
	for _, group := range req.Groups {
		if _, ok := counts[group]; ok {
			continue
		}
		accounts, err := h.adminService.ListAccountsForSchedulerScoreFilter(c.Request.Context(), service.PlatformOpenAI, "", "", "", group, "")
		if err != nil {
			response.InternalError(c, "unable to load detection scope")
			return
		}
		counts[group] = 0
		for i := range accounts {
			a := &accounts[i]
			if excluded[a.ID] || !a.IntelligenceMonitoringEligible(now) {
				continue
			}
			n, ok := seen[a.ID]
			if !ok {
				for _, model := range req.Models {
					if a.SupportsIntelligenceModel(model) {
						n++
					}
				}
				seen[a.ID] = n
				if n > 0 || len(req.Models) == 0 {
					ids = append(ids, a.ID)
					total += n
				}
			}
			if n > 0 || len(req.Models) == 0 {
				counts[group]++
			}
		}
	}
	slices.Sort(ids)
	c.JSON(http.StatusOK, gin.H{"account_ids": ids, "group_counts": counts, "targets": total})
}

func (h *ScheduledTestHandler) ReleaseIntelligenceHold(c *gin.Context) {
	admin, ok := testReviewAdmin(c)
	if !ok {
		return
	}
	id, ok := intelligenceID(c)
	if !ok {
		return
	}
	var req struct {
		AccountID int64  `json:"account_id" binding:"required,gt=0"`
		Model     string `json:"model" binding:"required,max=200"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "invalid account or model")
		return
	}
	err := h.scheduledTestSvc.ReleaseIntelligenceHold(c.Request.Context(), id, req.AccountID, req.Model, admin)
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "intelligence hold no longer active")
		return
	}
	if err != nil {
		response.InternalError(c, "unable to release intelligence hold")
		return
	}
	c.JSON(http.StatusOK, gin.H{"released": true})
}
