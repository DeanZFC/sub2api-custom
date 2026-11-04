package admin

import (
	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/gin-gonic/gin"
)

// Both routes are registered only within the authenticated admin route group.
func (h *OpenAIOAuthReauthHandler) CredentialEncryption(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	status, err := h.service.CredentialEncryptionStatus()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}

func (h *OpenAIOAuthReauthHandler) InitializeCredentialEncryption(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	status, err := h.service.InitializeCredentialEncryption()
	if err != nil {
		response.ErrorFrom(c, err)
		return
	}
	response.Success(c, status)
}
