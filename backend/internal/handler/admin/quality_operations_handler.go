package admin

import (
	"context"
	"database/sql"
	"errors"
	"net/http"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func qualityOperationsError(c *gin.Context, err error) {
	if errors.Is(err, service.ErrQualityOperationsConflict) {
		c.JSON(http.StatusConflict, gin.H{"error": "configuration changed; refresh and retry"})
		return
	}
	if errors.Is(err, sql.ErrNoRows) {
		response.NotFound(c, "quality configuration not found")
		return
	}
	response.InternalError(c, "quality operation failed")
}
func (h *ChannelQualityHandler) Operations(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
	defer cancel()
	out, err := h.operations.OperationsSnapshot(ctx)
	if err != nil {
		qualityOperationsError(c, err)
		return
	}
	c.JSON(http.StatusOK, out)
}
func (h *ChannelQualityHandler) SaveOperations(c *gin.Context) {
	var cfg service.QualityOperationsConfig
	if err := c.ShouldBindJSON(&cfg); err != nil {
		response.BadRequest(c, "invalid quality configuration")
		return
	}
	if err := cfg.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.operations.SaveOperationsConfig(c.Request.Context(), &cfg); err != nil {
		qualityOperationsError(c, err)
		return
	}
	c.JSON(http.StatusOK, cfg)
}
