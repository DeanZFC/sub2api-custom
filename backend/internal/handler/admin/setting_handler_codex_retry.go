package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func (h *SettingHandler) GetCodexPreOutputRetrySettings(c *gin.Context) {
	settings, err := h.settingService.GetCodexPreOutputRetrySettings(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, settings)
}

func (h *SettingHandler) UpdateCodexPreOutputRetrySettings(c *gin.Context) {
	var settings service.CodexPreOutputRetrySettings
	if err := c.ShouldBindJSON(&settings); err != nil {
		response.BadRequest(c, "Invalid request: "+err.Error())
		return
	}
	if err := settings.Validate(); err != nil {
		response.BadRequest(c, err.Error())
		return
	}
	if err := h.settingService.SetCodexPreOutputRetrySettings(c.Request.Context(), settings); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	h.GetCodexPreOutputRetrySettings(c)
}
