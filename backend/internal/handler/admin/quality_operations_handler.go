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
	if errors.Is(err, service.ErrPassiveQualityConflict) {
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
func (h *ChannelQualityHandler) SaveSupplier(c *gin.Context) {
	var supplier service.QualitySupplier
	if err := c.ShouldBindJSON(&supplier); err != nil {
		response.BadRequest(c, "invalid supplier")
		return
	}
	if c.Param("id") != "" {
		id, ok := passiveID(c, "id")
		if !ok {
			return
		}
		supplier.ID = id
	} else {
		supplier.ID = 0
		supplier.Version = 0
	}
	if err := supplier.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.operations.SaveSupplier(c.Request.Context(), &supplier); err != nil {
		if errors.Is(err, service.ErrPassiveQualityConflict) {
			qualityOperationsError(c, err)
		} else {
			response.BadRequest(c, "supplier save failed: check account membership and refresh")
		}
		return
	}
	c.JSON(http.StatusOK, supplier)
}
func (h *ChannelQualityHandler) DeleteSupplier(c *gin.Context) {
	id, ok := passiveID(c, "id")
	if !ok {
		return
	}
	version, ok := passiveQuery(c, "version")
	if !ok {
		return
	}
	if version <= 0 {
		response.BadRequest(c, "version is required")
		return
	}
	if err := h.operations.DeleteSupplier(c.Request.Context(), id, version); err != nil {
		qualityOperationsError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
