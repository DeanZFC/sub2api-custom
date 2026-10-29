package admin

import (
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// GetAccountRequestStatsBatch returns complete rolling-hour metrics for visible
// account rows without reading each account's paginated request details.
// POST /api/v1/admin/ops/account-request-stats/batch
func (h *OpsHandler) GetAccountRequestStatsBatch(c *gin.Context) {
	if h.opsService == nil {
		response.Error(c, http.StatusServiceUnavailable, "Ops service not available")
		return
	}
	var request struct {
		AccountIDs []int64 `json:"account_ids"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	stats, err := h.opsService.GetAccountRequestStatsBatch(c.Request.Context(), request.AccountIDs)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, stats)
}
