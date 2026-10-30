package middleware

import (
	"bytes"
	"errors"
	"mime"
	"net/http"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/httputil"
	"github.com/Wei-Shaw/sub2api/internal/pkg/requestmodel"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
)

// RequestModelFields rejects ambiguous top-level JSON model fields before any
// model allowlist, composite routing, or protocol conversion can choose a value.
// This validation is independent of whether a group enables its allowlist.
func RequestModelFields(maxBodySize, textMaxBodySize int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if c.Request == nil {
			c.Next()
			return
		}
		switch c.Request.Method {
		case http.MethodPost, http.MethodPut, http.MethodPatch:
		default:
			c.Next()
			return
		}
		if requestModelFieldsUsesNonJSONParser(c) {
			c.Next()
			return
		}
		readLimit := maxBodySize
		if requestModelFieldsTextRoute(c.FullPath()) && textMaxBodySize > 0 && (readLimit <= 0 || textMaxBodySize < readLimit) {
			readLimit = textMaxBodySize
			// The route's own textBodyLimit runs after this middleware. Enforce
			// it before prereading so an oversized text request is never buffered
			// up to the much larger inline-media limit first.
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, readLimit)
		}

		body, err := httputil.ReadRequestBodyWithPrealloc(c.Request)
		if err == nil {
			requestmodel.ResetRequestBody(c.Request, body)
			// Inspect the wire shape rather than Content-Type: Responses accepts
			// JSON even when clients omit or mislabel that header. Multipart and
			// SDP payloads retain their original bytes and parser behavior.
			probe := bytes.TrimSpace(body)
			probe = bytes.TrimPrefix(probe, []byte{0xef, 0xbb, 0xbf})
			probe = bytes.TrimSpace(probe)
			if len(probe) == 0 || probe[0] != '{' {
				c.Next()
				return
			}
			// Match the lenient JSON accepted by downstream handlers without
			// rewriting their input or letting a raw control byte hide duplicates.
			var normalized []byte
			normalized, err = httputil.NormalizeLenientJSONRequestBody(body, readLimit)
			if err == nil {
				if validationErr := requestmodel.ValidateJSONModelFields(normalized); validationErr != nil {
					requestModelFieldsError(c, http.StatusBadRequest, validationErr.Error())
					c.Abort()
					return
				}
				c.Next()
				return
			}
		}

		status := http.StatusBadRequest
		message := "Failed to read request body"
		var maxErr *http.MaxBytesError
		if errors.As(err, &maxErr) {
			status = http.StatusRequestEntityTooLarge
			message = "Request body is too large"
		}
		requestModelFieldsError(c, status, message)
		c.Abort()
	}
}

// Only exempt routes whose selected handler treats the declared content type as
// non-JSON. JSON-only endpoints must still validate mislabeled request bodies.
func requestModelFieldsUsesNonJSONParser(c *gin.Context) bool {
	mediaType, _, err := mime.ParseMediaType(c.GetHeader("Content-Type"))
	if err != nil {
		return false
	}
	mediaType = strings.ToLower(mediaType)
	path := c.FullPath()
	if mediaType == "multipart/form-data" {
		switch path {
		case "/v1/live", "/backend-api/codex/realtime/calls":
			return true
		case "/v1/images/generations", "/v1/images/edits", "/images/generations", "/images/edits":
			// Grok probes JSON before multipart, and Composite is not resolved
			// yet. Their bodies must still be checked regardless of this header.
			apiKey, ok := GetAPIKeyFromContext(c)
			return ok && apiKey != nil && apiKey.Group != nil && apiKey.Group.Platform == service.PlatformOpenAI
		}
	}
	switch path {
	case "/v1/stt", "/stt", "/v1/custom-voices", "/custom-voices", "/v1/custom-voices/:voice_id", "/custom-voices/:voice_id":
		// Voice uploads are opaque bodies and use a fixed routing model. Leave
		// reading to the existing handler after its platform/balance checks.
		return mediaType == "multipart/form-data" || mediaType == "application/octet-stream" || strings.HasPrefix(mediaType, "audio/")
	}
	return false
}

func requestModelFieldsTextRoute(path string) bool {
	switch path {
	case "/v1/systemone", "/v1/alpha/search", "/alpha/search", "/backend-api/codex/alpha/search", "/v1/embeddings", "/embeddings":
		return true
	default:
		return false
	}
}

func requestModelFieldsError(c *gin.Context, status int, message string) {
	path := c.Request.URL.Path
	if strings.HasPrefix(path, "/v1beta") || strings.HasPrefix(path, "/antigravity/v1beta") {
		GoogleErrorWriter(c, status, message)
		return
	}
	errorBody := gin.H{"error": gin.H{"type": "invalid_request_error", "message": message}}
	if strings.Contains(path, "/messages") {
		errorBody["type"] = "error"
	}
	c.JSON(status, errorBody)
}
