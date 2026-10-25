package admin

import (
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type OpenAIVMHandler struct{ service *service.OpenAIVMService }

func NewOpenAIVMHandler(s *service.OpenAIVMService) *OpenAIVMHandler {
	return &OpenAIVMHandler{service: s}
}
func (h *OpenAIVMHandler) GetConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	v, err := h.service.GetConfig(c.Request.Context())
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
func (h *OpenAIVMHandler) SaveConfig(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var in service.OpenAIVMConfigInput
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid bridge configuration")
		return
	}
	v, err := h.service.SaveConfig(c.Request.Context(), in)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
func (h *OpenAIVMHandler) Test(c *gin.Context) {
	if err := h.service.Test(c.Request.Context()); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"healthy": true, "message": "Bridge authenticated"})
}
func openAIVMAccountParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		response.BadRequest(c, "Invalid account ID")
		return 0, false
	}
	return id, true
}
func (h *OpenAIVMHandler) Status(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	id, ok := openAIVMAccountParam(c)
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
func (h *OpenAIVMHandler) Create(c *gin.Context) {
	id, ok := openAIVMAccountParam(c)
	if !ok {
		return
	}
	var in struct {
		Runtime string `json:"runtime_type"`
	}
	if c.ShouldBindJSON(&in) != nil {
		response.BadRequest(c, "Invalid VM runtime")
		return
	}
	v, err := h.service.Create(c.Request.Context(), id, in.Runtime)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
func (h *OpenAIVMHandler) Delete(c *gin.Context) {
	id, ok := openAIVMAccountParam(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id); err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, gin.H{"message": "VM slot detached; original route restored"})
}
func (h *OpenAIVMHandler) Retry(c *gin.Context) {
	id, ok := openAIVMAccountParam(c)
	if !ok {
		return
	}
	v, err := h.service.Retry(c.Request.Context(), id)
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, v)
}
