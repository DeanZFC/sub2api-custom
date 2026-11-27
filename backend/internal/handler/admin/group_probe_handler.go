package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type GroupProbeHandler struct{ s *service.GroupProbeService }

func NewGroupProbeHandler(s *service.GroupProbeService) *GroupProbeHandler {
	return &GroupProbeHandler{s}
}
func (h *GroupProbeHandler) List(c *gin.Context) {
	list, err := h.s.List(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, list)
}
func (h *GroupProbeHandler) Configure(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	var body struct {
		service.GroupProbeRuleUpdate
		Model   string `json:"model" binding:"required"`
		Enabled *bool  `json:"enabled" binding:"required"`
	}
	if err != nil || id <= 0 || c.ShouldBindJSON(&body) != nil {
		response.BadRequest(c, "invalid group probe configuration")
		return
	}
	if err = h.s.Configure(c.Request.Context(), id, body.Model, *body.Enabled, body.GroupProbeRuleUpdate); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}
func (h *GroupProbeHandler) Run(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "invalid group ID")
		return
	}
	if err = h.s.Request(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, nil)
}
