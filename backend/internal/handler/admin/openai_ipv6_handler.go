package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OpenAIIPv6Handler struct{ service *service.OpenAIIPv6Service }

func NewOpenAIIPv6Handler(s *service.OpenAIIPv6Service) *OpenAIIPv6Handler {
	return &OpenAIIPv6Handler{s}
}
func (h *OpenAIIPv6Handler) Status(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, ok := managedEgressAccountParam(c)
	if !ok {
		return
	}
	v, err := h.service.Status(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
func (h *OpenAIIPv6Handler) Bind(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, ok := managedEgressAccountParam(c)
	if !ok {
		return
	}
	v, err := h.service.Bind(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
func (h *OpenAIIPv6Handler) Detach(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, ok := managedEgressAccountParam(c)
	if !ok {
		return
	}
	if err := h.service.Detach(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"detached": true})
}

func managedEgressAccountParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}
