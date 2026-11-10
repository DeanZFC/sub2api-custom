package admin

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

func groupSmartWSID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "分组 ID 无效")
		return 0, false
	}
	return id, true
}

// GetGroupSmartWS is registered inside the existing administrator-only group.
func (h *SettingHandler) GetGroupSmartWS(c *gin.Context) {
	id, ok := groupSmartWSID(c)
	if !ok {
		return
	}
	policy, err := h.settingService.GetGroupSmartWS(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"enabled": policy.Enabled, "revision": policy.Revision, "runtime": service.GroupSmartWSRuntimeStats()})
}

func (h *SettingHandler) UpdateGroupSmartWS(c *gin.Context) {
	id, ok := groupSmartWSID(c)
	if !ok {
		return
	}
	var input struct {
		Enabled  *bool  `json:"enabled"`
		Revision *int64 `json:"revision"`
	}
	decoder := json.NewDecoder(http.MaxBytesReader(c.Writer, c.Request.Body, 1024))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&input); err != nil {
		response.BadRequest(c, "WS 设置必须包含 enabled 布尔值和 revision")
		return
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF || input.Enabled == nil || input.Revision == nil || *input.Revision < 0 {
		response.BadRequest(c, "WS 设置格式无效，请重新加载后保存")
		return
	}
	policy, err := h.settingService.UpdateGroupSmartWS(c.Request.Context(), id, *input.Enabled, *input.Revision)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, policy)
}
