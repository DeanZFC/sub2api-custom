package admin

import (
	"context"
	"net/http"
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
