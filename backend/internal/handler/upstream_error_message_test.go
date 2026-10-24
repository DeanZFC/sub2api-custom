package handler

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

type errorMessageSettingsRepo struct {
	service.SettingRepository
	message string
}

func (r *errorMessageSettingsRepo) GetValue(_ context.Context, key string) (string, error) {
	if key == service.SettingKeyUpstreamErrorMessage {
		return r.message, nil
	}
	return "", service.ErrSettingNotFound
}

func TestConfiguredUpstreamErrorHTTPAndSSE(t *testing.T) {
	const raw = "Excel BPS private worker failed"
	const message = "服务暂时不可用，请稍后再试。"
	for _, tc := range []struct {
		name, path, contentType, body, wantPath string
		status                                  int
		sse, upstream                           bool
	}{
		{"openai http", "/v1/chat/completions", "application/json", `{"error":{"type":"invalid_request_error","code":"invalid_parameter","message":"` + raw + `","details":"` + raw + `"},"debug":"` + raw + `"}`, "error.message", 400, false, true},
		{"anthropic http", "/v1/messages", "application/json", `{"type":"error","error":{"type":"overloaded_error","message":"` + raw + `"}}`, "error.message", 529, false, true},
		{"gemini http", "/v1beta/models/m:generateContent", "application/json", `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"` + raw + `","details":[{"reason":"` + raw + `"}]}}`, "error.message", 403, false, true},
		{"raw chat http200", "/v1/chat/completions", "application/json", `{"error":{"message":"` + raw + `"}}`, "error.message", 200, false, false},
		{"provider billing", "/v1/messages", "application/json", `{"error":{"type":"billing_error","message":"` + raw + `"}}`, "error.message", 402, false, true},
		{"video task failure", "/v1/videos/request_1", "application/json", `{"request_id":"request_1","status":"failed","message":"` + raw + `","details":"` + raw + `"}`, "message", 200, false, false},
		{"provider html", "/v1/messages", "text/html", `<html>` + raw + `</html>`, "error.message", 502, false, true},
		{"provider text", "/v1beta/models/m:generateContent", "text/plain", raw, "error.message", 502, false, true},
		{"responses failed", "/v1/responses", "text/event-stream", "event: response.failed\ndata: " + `{"type":"response.failed","response":{"error":{"code":"context_length_exceeded","message":"` + raw + `"}}}` + "\n\n", "response.error.message", 200, true, true},
		{"anthropic native unrecorded", "/v1/messages", "text/event-stream", "event: error\ndata: " + `{"type":"error","error":{"type":"invalid_request_error","message":"` + raw + `"}}` + "\n\n", "error.message", 200, true, false},
		{"gemini native unrecorded", "/v1beta/models/m:streamGenerateContent", "text/event-stream", "data: " + `{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"` + raw + `"}}` + "\r\n\r\n", "error.message", 200, true, false},
		{"raw chat sse", "/v1/chat/completions", "text/event-stream", "data: " + `{"error":{"type":"server_error","message":"` + raw + `"}}` + "\n\n", "error.message", 200, true, false},
		{"malformed error event", "/v1/messages", "text/event-stream", "event: error\ndata: " + raw + "\n\n", "error.message", 200, true, false},
		{"cr only", "/v1/messages", "text/event-stream", "event: error\rdata: " + `{"type":"error","error":{"message":"` + raw + `"}}` + "\r\r", "error.message", 200, true, false},
	} {
		for _, split := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/split=%t", tc.name, split), func(t *testing.T) {
				gin.SetMode(gin.TestMode)
				router := gin.New()
				router.Use(UpstreamErrorMessageMiddleware(service.NewSettingService(&errorMessageSettingsRepo{message: message}, nil)))
				var captured string
				router.Use(func(c *gin.Context) {
					// Same capture order as OpsErrorLoggerMiddleware, without queues.
					original := c.Writer
					w := acquireOpsCaptureWriter(original)
					w.setContext(c)
					c.Writer = w
					c.Next()
					w.finalizeCapture()
					captured = string(w.capturedBytes())
					if terminal, ok := w.capturedTerminalError(); ok {
						captured += terminal.Message
					}
					c.Writer = original
					releaseOpsCaptureWriter(w)
				})
				router.POST(tc.path, func(c *gin.Context) {
					setOpsSelectedAccount(c, 123, service.PlatformOpenAI)
					if tc.upstream {
						service.SetOpsUpstreamError(c, tc.status, raw, tc.body)
					}
					c.Header("Content-Type", tc.contentType)
					c.Header("Content-Length", strconv.Itoa(len(tc.body)))
					c.Status(tc.status)
					if split {
						for _, b := range []byte(tc.body) {
							_, err := c.Writer.Write([]byte{b})
							require.NoError(t, err)
							if tc.sse {
								c.Writer.Flush()
							}
						}
					} else {
						_, err := c.Writer.WriteString(tc.body)
						require.NoError(t, err)
					}
				})
				rec := httptest.NewRecorder()
				router.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, tc.path, nil))
				require.Equal(t, tc.status, rec.Code)
				require.NotContains(t, rec.Body.String(), raw)
				require.Contains(t, rec.Body.String(), message)
				require.Empty(t, rec.Header().Get("Content-Length"))
				if tc.status >= 400 || (tc.sse && tc.name != "malformed error event") {
					require.Contains(t, captured, raw, "administrator capture must precede client wrapping")
				}
				body := rec.Body.String()
				if tc.sse {
					body = strings.TrimSpace(strings.SplitN(body, "data: ", 2)[1])
				}
				require.Equal(t, message, gjson.Get(body, tc.wantPath).String())
				if tc.name == "responses failed" {
					require.True(t, gjson.Get(body, "response.output").IsArray())
					require.True(t, gjson.Get(body, "sequence_number").Exists())
				}
				if tc.name == "video task failure" {
					require.Equal(t, "request_1", gjson.Get(body, "request_id").String())
					require.Equal(t, "failed", gjson.Get(body, "status").String())
				}
			})
		}
	}
}

func TestConfiguredUpstreamErrorPreservesLocalAndSuccess(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		status     int
		sse, stale bool
	}{
		{"auth", `{"error":{"type":"authentication_error","message":"Invalid API key"}}`, 401, false, false},
		{"local parameter", `{"error":{"type":"invalid_request_error","message":"Missing model"}}`, 400, false, false},
		{"local internal", `{"error":{"type":"api_error","message":"Invalid gateway configuration"}}`, 500, false, false},
		{"local quota after retry", `{"error":{"type":"billing_error","message":"Insufficient balance"}}`, 403, false, true},
		{"local stream quota", "event: response.failed\ndata: " + `{"type":"response.failed","response":{"error":{"code":"billing_error","message":"Insufficient balance"}}}` + "\n\n", 200, true, true},
		{"assistant json", `{"choices":[{"message":{"content":"error: Excel BPS is a phrase"}}],"usage":{"total_tokens":7}}`, 200, false, false},
		{"assistant stream", ": ping\n\ndata: " + `{"choices":[{"delta":{"content":"error: Excel BPS"}}]}` + "\n\ndata: [DONE]\n\n", 200, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := gin.New()
			r.Use(UpstreamErrorMessageMiddleware(service.NewSettingService(&errorMessageSettingsRepo{message: "统一提示"}, nil)))
			r.POST("/test", func(c *gin.Context) {
				if tc.stale {
					service.SetOpsUpstreamError(c, 502, "old failure", "")
				}
				contentType := "application/json"
				if tc.sse {
					contentType = "text/event-stream"
				}
				c.Data(tc.status, contentType, []byte(tc.body))
			})
			rec := httptest.NewRecorder()
			r.ServeHTTP(rec, httptest.NewRequest("POST", "/test", nil))
			require.Equal(t, tc.body, rec.Body.String())
		})
	}
}

func TestConfiguredUpstreamErrorDisabled(t *testing.T) {
	r := gin.New()
	r.Use(UpstreamErrorMessageMiddleware(nil))
	const body = `{"error":{"message":"Original provider error"}}`
	r.POST("/test", func(c *gin.Context) { c.Data(502, "application/json", []byte(body)) })
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest("POST", "/test", nil))
	require.Equal(t, body, rec.Body.String())
}
