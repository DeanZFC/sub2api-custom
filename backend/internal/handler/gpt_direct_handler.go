package handler

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"

	"github.com/Wei-Shaw/sub2api/internal/pkg/response"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

type GPTDirectHandler struct {
	settings *service.SettingService
	client   *service.GPTDirectService
}

func NewGPTDirectHandler(settings *service.SettingService, client *service.GPTDirectService) *GPTDirectHandler {
	return &GPTDirectHandler{settings: settings, client: client}
}

func (h *GPTDirectHandler) GetSettings(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	config, err := h.settings.GetGPTDirectConfig(c.Request.Context())
	if err != nil {
		response.Error(c, 503, "GPT direct settings unavailable")
		return
	}
	response.Success(c, config)
}

func (h *GPTDirectHandler) SaveSettings(c *gin.Context) {
	var config service.GPTDirectConfig
	if !bindGPTDirectJSON(c, &config) {
		return
	}
	if config.LotteryRewardEnabled && !h.client.RewardsReady() {
		response.ErrorWithDetails(c, 400, "Configure a persistent encryption key before enabling lottery rewards", "REWARD_UNAVAILABLE", nil)
		return
	}
	saved, err := h.settings.SaveGPTDirectConfig(c.Request.Context(), config)
	if err != nil {
		response.ErrorWithDetails(c, 400, "Check GPT direct settings and URLs", "CONFIG_INVALID", nil)
		return
	}
	response.Success(c, saved)
}

func (h *GPTDirectHandler) Config(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	config, err := h.settings.GetGPTDirectConfig(c.Request.Context())
	if err != nil {
		response.ErrorWithDetails(c, 503, "GPT direct settings unavailable", "CONFIG_UNAVAILABLE", nil)
		return
	}
	response.Success(c, config.Public())
}

func bindGPTDirectJSON(c *gin.Context, value any) bool {
	c.Header("Cache-Control", "no-store")
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 96*1024)
	decoder := json.NewDecoder(c.Request.Body)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(value); err != nil {
		response.ErrorWithDetails(c, 400, "Invalid request", "VALIDATION_ERROR", nil)
		return false
	}
	if decoder.Decode(new(any)) != io.EOF {
		response.ErrorWithDetails(c, 400, "Invalid request", "VALIDATION_ERROR", nil)
		return false
	}
	return true
}

// Operation only receives server-owned operation names from route registration.
func (h *GPTDirectHandler) Operation(operation string) gin.HandlerFunc {
	return func(c *gin.Context) {
		subject, ok := middleware.GetAuthSubjectFromContext(c)
		if !ok {
			response.Unauthorized(c, "User not authenticated")
			return
		}
		var input service.GPTDirectInput
		if !bindGPTDirectJSON(c, &input) {
			return
		}
		result, failure := h.client.Call(c.Request.Context(), operation, input, subject.UserID, middleware.SecurityClientIP(c), c.GetHeader("Idempotency-Key"))
		if failure != nil {
			var metadata map[string]string
			if failure.RetryAfter > 0 {
				delay := strconv.Itoa(failure.RetryAfter)
				c.Header("Retry-After", delay)
				metadata = map[string]string{"retry_after": delay}
			}
			response.ErrorWithDetails(c, failure.Status, "GPT direct request could not be completed", failure.Reason, metadata)
			return
		}
		response.Success(c, result)
	}
}
