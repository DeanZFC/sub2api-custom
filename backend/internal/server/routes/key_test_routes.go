package routes

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/domain"
	"github.com/Wei-Shaw/sub2api/internal/handler"
	pkghttputil "github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RegisterAPIKeyTestRoutes exposes a small, billed text request for an owned key.
// The login session authorizes key ownership; all gateway key policies, routing,
// concurrency, usage recording and billing still run against that actual key.
func RegisterAPIKeyTestRoutes(
	r *gin.Engine, h *handler.Handlers,
	jwtAuth middleware.JWTAuthMiddleware, panelKeyAuth gin.HandlerFunc,
	settings *service.SettingService, ops *service.OpsService,
	resolver *service.CompositeRouteResolver,
	limiter *middleware.PanelRateLimiter,
) {
	tests := r.Group("/api/v1/key-tests/:key_id")
	tests.Use(gin.HandlerFunc(jwtAuth), middleware.BackendModeUserGuard(settings))
	tests.Use(limiter.Global(), limiter.Heavy())
	tests.Use(middleware.RequestBodyLimit(16*1024), middleware.ClientRequestID())
	tests.Use(handler.UpstreamErrorMessageMiddleware(settings), handler.OpsErrorLoggerMiddleware(ops))
	tests.Use(panelKeyAuth)
	tests.Use(middleware.RequestModelFields(16*1024, 16*1024))
	tests.Use(prepareAPIKeyTestRequest())
	tests.Use(handler.InboundEndpointMiddleware(), middleware.UpstreamErrorRetry(settings))
	tests.Use(middleware.GroupModelAllowlist(), compositeTargetPlatformMiddleware(resolver))
	tests.Use(middleware.RequireGroupAssignment(settings, middleware.AnthropicErrorWriter))
	tests.GET("/models", h.Gateway.Models)
	tests.POST("", func(c *gin.Context) {
		if domain.UsesOpenAIGateway(getGroupPlatform(c)) {
			h.OpenAIGateway.ChatCompletions(c)
			return
		}
		h.Gateway.ChatCompletions(c)
	})
}

// Only accept a model. Use the same fixed "hi" probe as account text tests; never
// accept client-supplied routing, tools, streaming, or token-limit overrides.
func prepareAPIKeyTestRequest() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		path := "/v1/models"
		if c.Request.Method == http.MethodPost {
			var input struct {
				Model string `json:"model"`
			}
			body, err := pkghttputil.ReadRequestBodyWithPrealloc(c.Request)
			if err != nil {
				middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_TEST_REQUEST", "Failed to read test request")
				return
			}
			decoder := json.NewDecoder(bytes.NewReader(body))
			decoder.DisallowUnknownFields()
			if err = decoder.Decode(&input); err != nil {
				middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_TEST_REQUEST", "Only model is accepted")
				return
			}
			var extra any
			if decoder.Decode(&extra) != io.EOF {
				middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_TEST_REQUEST", "Expected one JSON object")
				return
			}
			input.Model = strings.TrimSpace(input.Model)
			if input.Model == "" || len(input.Model) > 200 {
				middleware.AbortWithError(c, http.StatusBadRequest, "INVALID_TEST_REQUEST", "A model name of 1 to 200 bytes is required")
				return
			}
			payload, _ := json.Marshal(gin.H{
				"model":      input.Model,
				"messages":   []gin.H{{"role": "user", "content": "hi"}},
				"max_tokens": 256,
				"stream":     false,
			})
			requestmodel.ResetRequestBody(c.Request, payload)
			c.Request.Header.Set("Content-Type", "application/json")
			path = "/v1/chat/completions"
		}
		// Canonical paths preserve composite endpoint routing and usage labels.
		originalURL := c.Request.URL
		canonicalURL := *originalURL
		canonicalURL.Path = path
		canonicalURL.RawPath = ""
		canonicalURL.RawQuery = ""
		c.Request.URL = &canonicalURL
		defer func() { c.Request.URL = originalURL }()
		c.Next()
	}
}
