package admin

import (
	"errors"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// AccountHealthHandler exposes the opt-in account health observer to admins.
type AccountHealthHandler struct {
	service *service.AccountHealthService
}

func NewAccountHealthHandler(svc *service.AccountHealthService) *AccountHealthHandler {
	return &AccountHealthHandler{service: svc}
}

func (h *AccountHealthHandler) requireService(c *gin.Context) bool {
	if h == nil || h.service == nil {
		response.ErrorFrom(c, errors.New("account health service unavailable"))
		return false
	}
	return true
}

// Snapshot returns the most recent health evaluation.
func (h *AccountHealthHandler) Snapshot(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	items := h.service.Snapshot()
	if items == nil {
		items = []service.AccountHealthSnapshot{}
	}
	response.Success(c, gin.H{"items": items, "count": len(items)})
}

func (h *AccountHealthHandler) GetSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	response.Success(c, h.service.GetSettings(c.Request.Context()))
}

func (h *AccountHealthHandler) UpdateSettings(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	var req service.AccountHealthSettings
	if err := c.ShouldBindJSON(&req); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	out, err := h.service.UpdateSettings(c.Request.Context(), req)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, out)
}

func (h *AccountHealthHandler) Isolate(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.service.Isolate(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Account isolated successfully"})
}

func (h *AccountHealthHandler) Resume(c *gin.Context) {
	if !h.requireService(c) {
		return
	}
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return
	}
	if err := h.service.Resume(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "Account resumed successfully"})
}
