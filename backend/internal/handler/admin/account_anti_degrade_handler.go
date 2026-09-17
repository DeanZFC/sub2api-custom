package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// ListAntiDegradeStrategies returns the server-owned account protection
// strategy registry. The route is static and is registered before /:id.
func (h *AccountHandler) ListAntiDegradeStrategies(c *gin.Context) {
	response.Success(c, gin.H{"strategies": service.ListAntiDegradeStrategyProfiles()})
}

func (h *AccountHandler) antiDegradeService() *service.AntiDegradeService {
	if h == nil || h.adminService == nil {
		return nil
	}
	return service.NewAntiDegradeService(h.adminService)
}

func parseAntiDegradeAccountID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}

// PreviewAntiDegrade computes the requested strategy without writing account
// state. mode is optional; the service applies its legacy default.
func (h *AccountHandler) PreviewAntiDegrade(c *gin.Context) {
	id, ok := parseAntiDegradeAccountID(c)
	if !ok {
		return
	}
	svc := h.antiDegradeService()
	if svc == nil {
		response.BadRequest(c, "account protection service unavailable")
		return
	}
	preview, err := svc.PreviewMode(c.Request.Context(), id, service.AntiDegradeMode(c.Query("mode")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, preview)
}

// ApplyAntiDegrade applies a strategy immediately. It changes the persisted
// account configuration and does not require credential re-import.
func (h *AccountHandler) ApplyAntiDegrade(c *gin.Context) {
	id, ok := parseAntiDegradeAccountID(c)
	if !ok {
		return
	}
	svc := h.antiDegradeService()
	if svc == nil {
		response.BadRequest(c, "account protection service unavailable")
		return
	}
	account, err := svc.ApplyAntiDegradeMode(c.Request.Context(), id, service.AntiDegradeMode(c.Query("mode")))
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.buildAccountResponseWithRuntime(c.Request.Context(), account))
}

// RevertAntiDegrade restores the snapshot captured when protection was
// applied. The explicit confirmation prevents accidental disabling.
func (h *AccountHandler) RevertAntiDegrade(c *gin.Context) {
	id, ok := parseAntiDegradeAccountID(c)
	if !ok {
		return
	}
	var req struct {
		ConfirmDisable bool `json:"confirm_disable"`
	}
	if err := c.ShouldBindJSON(&req); err != nil || !req.ConfirmDisable {
		response.BadRequest(c, "关闭防降智模式需要管理员明确确认")
		return
	}
	svc := h.antiDegradeService()
	if svc == nil {
		response.BadRequest(c, "account protection service unavailable")
		return
	}
	account, err := svc.Revert(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, h.buildAccountResponseWithRuntime(c.Request.Context(), account))
}
