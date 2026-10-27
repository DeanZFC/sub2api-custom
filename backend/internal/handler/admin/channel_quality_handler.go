package admin

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type ChannelQualityHandler struct {
	repo       service.ChannelQualityRepository
	operations service.QualityOperationsRepository
}

func NewChannelQualityHandler(repo service.ChannelQualityRepository, operations service.QualityOperationsRepository) *ChannelQualityHandler {
	return &ChannelQualityHandler{repo: repo, operations: operations}
}

func (h *ChannelQualityHandler) ScoreConfig(c *gin.Context) {
	repo, ok := h.repo.(service.ChannelQualityScoreSettingsRepository)
	if !ok {
		response.InternalError(c, "quality score settings unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	config, err := repo.GetQualityScoreConfig(ctx)
	if err != nil {
		response.InternalError(c, "unable to load quality score settings")
		return
	}
	c.JSON(http.StatusOK, config)
}

func (h *ChannelQualityHandler) SaveScoreConfig(c *gin.Context) {
	var config service.ChannelQualityScoreConfig
	if err := c.ShouldBindJSON(&config); err != nil {
		response.BadRequest(c, "invalid quality score configuration")
		return
	}
	if err := config.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if config.Version <= 0 {
		response.BadRequest(c, "configuration version is required")
		return
	}
	repo, ok := h.repo.(service.ChannelQualityScoreSettingsRepository)
	if !ok {
		response.InternalError(c, "quality score settings unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Second)
	defer cancel()
	if err := repo.SaveQualityScoreConfig(ctx, &config); err != nil {
		if errors.Is(err, service.ErrChannelQualityScoreConflict) {
			response.Error(c, http.StatusConflict, "quality score configuration changed; refresh and retry")
		} else {
			response.InternalError(c, "unable to save quality score settings")
		}
		return
	}
	c.JSON(http.StatusOK, config)
}

// Scores shares the overview's score provider with the paginated account list.
func (h *ChannelQualityHandler) Scores(c *gin.Context) {
	parts := strings.Split(c.Query("account_ids"), ",")
	if len(parts) > 100 {
		response.BadRequest(c, "at most 100 account ids are allowed")
		return
	}
	ids := make([]int64, 0, len(parts))
	seen := make(map[int64]bool, len(parts))
	for _, part := range parts {
		id, err := strconv.ParseInt(strings.TrimSpace(part), 10, 64)
		if err != nil || id <= 0 {
			response.BadRequest(c, "positive account ids are required")
			return
		}
		if !seen[id] {
			ids = append(ids, id)
			seen[id] = true
		}
	}
	model := strings.TrimSpace(c.Query("model"))
	if len(model) > 200 {
		response.BadRequest(c, "model must be at most 200 characters")
		return
	}
	provider, ok := h.repo.(service.ChannelQualityScoreProvider)
	if !ok {
		response.InternalError(c, "quality scores unavailable")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	scores, err := provider.ChannelQualityScores(ctx, ids, model)
	if err != nil {
		response.InternalError(c, "unable to load quality scores")
		return
	}
	c.JSON(http.StatusOK, scores)
}

func (h *ChannelQualityHandler) Overview(c *gin.Context) {
	account, ok := passiveQuery(c, "account_id")
	if !ok {
		return
	}
	group, ok := passiveQuery(c, "group_id")
	if !ok {
		return
	}
	after, ok := passiveQuery(c, "after")
	if !ok {
		return
	}
	search, model := strings.TrimSpace(c.Query("search")), strings.TrimSpace(c.Query("model"))
	if len(search) > 200 || len(model) > 200 {
		response.BadRequest(c, "search and model must be at most 200 characters")
		return
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	page, err := h.repo.ListQualityAccounts(ctx, service.ChannelQualityFilter{Search: search, Model: model, GroupID: group, AccountID: account, After: after, Limit: 50, ProblemsOnly: c.Query("problems_only") == "true"})
	if err != nil {
		response.InternalError(c, "unable to load channel quality")
		return
	}
	c.JSON(http.StatusOK, page)
}
