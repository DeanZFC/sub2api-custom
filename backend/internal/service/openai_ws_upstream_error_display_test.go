package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Wei-Shaw/sub2api/internal/config"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const openAIWSCustomFailureMessage = "服务暂时不可用，请联系管理员处理。"

const openAIWSInternalFailureExample = "stream disconnected before completion: Excel BPS did not deliver validated output within the allowed interval"

func TestForwardWSV2WrapsInternalUpstreamErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, sample := range []struct {
		name, errType, code, message, configured string
	}{
		{"default_internal", "upstream_error", "stream_error", openAIWSInternalFailureExample, ""},
		{"configured_request", "invalid_request_error", "invalid_request", "The input field is required", openAIWSCustomFailureMessage},
		{"configured_context", "invalid_request_error", "context_length_exceeded", "Your input exceeds the context window", openAIWSCustomFailureMessage},
		{"configured_policy", "invalid_request_error", "cyber_policy", "Request blocked by content policy", openAIWSCustomFailureMessage},
	} {
		for _, stream := range []bool{false, true} {
			for _, eventType := range []string{"error", "response.failed"} {
				t.Run(fmt.Sprintf("%s_stream_%v_%s", sample.name, stream, eventType), func(t *testing.T) {
					errorBody := map[string]any{
						"type": sample.errType, "code": sample.code, "message": sample.message,
						"details": "private backend stack trace",
					}
					failure := map[string]any{"type": eventType, "error": errorBody}
					if eventType == "response.failed" {
						failure = map[string]any{"type": eventType, "response": map[string]any{
							"id": "resp_failure", "model": "gpt-5.5", "error": errorBody,
							"usage": map[string]any{"input_tokens": 9, "output_tokens": 2},
						}}
					}
					failureJSON, err := json.Marshal(failure)
					require.NoError(t, err)
					events := [][]byte{failureJSON}
					if stream {
						events = append([][]byte{[]byte(`{"type":"response.output_text.delta","delta":"hello"}`)}, events...)
					}
					cfg := newOpenAIWSV2TestConfig()
					cfg.Security.URLAllowlist.Enabled = false
					cfg.Gateway.OpenAIWS.MinIdlePerAccount = 0
					pool := newOpenAIWSConnPool(cfg)
					pool.setClientDialerForTest(&openAIWSCaptureDialer{conn: &openAIWSCaptureConn{events: events}})
					svc := &OpenAIGatewayService{
						cfg: cfg, httpUpstream: &httpUpstreamRecorder{}, cache: &stubGatewayCache{},
						openaiWSResolver: NewOpenAIWSProtocolResolver(cfg), toolCorrector: NewCodexToolCorrector(), openaiWSPool: pool,
					}
					account := &Account{
						ID: 1337, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
						Schedulable: true, Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test"},
						Extra: map[string]any{"responses_websockets_v2_enabled": true},
					}
					rec := httptest.NewRecorder()
					c, _ := gin.CreateTestContext(rec)
					c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", nil)
					c.Request.Header.Set("User-Agent", "codex_cli_rs/0.98.0")
					ctx := WithUpstreamErrorMessage(context.Background(), sample.configured)
					c.Request = c.Request.WithContext(ctx)
					result, err := svc.Forward(ctx, c, account,
						[]byte(fmt.Sprintf(`{"model":"gpt-5.5","stream":%t,"input":"hello"}`, stream)))

					wantMessage := sample.configured
					if wantMessage == "" {
						wantMessage = OpenAIUpstreamFailureClientMessage
					}
					require.Contains(t, rec.Body.String(), wantMessage)
					require.NotContains(t, rec.Body.String(), sample.message)
					require.NotContains(t, rec.Body.String(), "Excel BPS")
					require.NotContains(t, rec.Body.String(), "private backend")
					if stream {
						require.Contains(t, rec.Body.String(), `"delta":"hello"`)
					}
					if eventType == "error" {
						require.ErrorContains(t, err, sample.message)
						require.Equal(t, sample.message, c.GetString(OpsUpstreamErrorMessageKey))
					} else {
						require.NoError(t, err)
						require.NotNil(t, result)
						require.False(t, result.SucceededForScheduling())
						require.Equal(t, 9, result.Usage.InputTokens)
						require.Equal(t, 2, result.Usage.OutputTokens)
						opsErr, ok := GetOpsStreamError(c)
						require.True(t, ok)
						require.Equal(t, sample.message, opsErr.Message)
					}
				})
			}
		}
	}
}

func TestWSHTTPBridgeWrapsInternalErrorsAndPreservesActionableErrors(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, configured := range []string{"", openAIWSCustomFailureMessage} {
		for _, tt := range []struct {
			name, eventType, errorType, code, message, wantMessage string
			status                                                 int
		}{
			{"http_internal", "", "server_error", "server_error", openAIWSInternalFailureExample, OpenAIUpstreamFailureClientMessage, http.StatusInternalServerError},
			{"http_invalid_request", "", "invalid_request_error", "invalid_request", "The input field is required", "The input field is required", http.StatusBadRequest},
			{"sse_failed", "response.failed", "server_error", "server_error", openAIWSInternalFailureExample, OpenAIUpstreamFailureClientMessage, http.StatusOK},
			{"sse_bare_error", "error", "server_error", "server_error", openAIWSInternalFailureExample, OpenAIUpstreamFailureClientMessage, http.StatusOK},
		} {
			t.Run(tt.name+"_configured_"+fmt.Sprint(configured != ""), func(t *testing.T) {
				errorBody := map[string]any{"type": tt.errorType, "code": tt.code, "message": tt.message, "details": "private backend stack trace"}
				failure := map[string]any{"error": errorBody}
				if tt.eventType != "" {
					failure["type"] = tt.eventType
				}
				if tt.eventType == "response.failed" {
					failure = map[string]any{"type": tt.eventType, "response": map[string]any{
						"id": "resp_bridge_failure", "error": errorBody, "usage": map[string]any{"input_tokens": 9, "output_tokens": 2},
					}}
				}
				failureJSON, err := json.Marshal(failure)
				require.NoError(t, err)
				body := string(failureJSON)
				if tt.eventType != "" {
					body = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_bridge_failure\"}}\n\n" +
						"data: " + body + "\n\n"
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: tt.status, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(body)),
				}}
				svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: upstream}
				account := &Account{ID: 1340, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Concurrency: 1}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodGet, "/v1/responses", nil)
				payload := []byte(`{"type":"response.create","model":"gpt-5.5","input":"hi"}`)
				var writes [][]byte
				ctx := WithUpstreamErrorMessage(context.Background(), configured)
				c.Request = c.Request.WithContext(ctx)
				result, err := svc.proxyOpenAIWSHTTPBridgeTurn(ctx, c, account, "sk-test", payload, len(payload),
					"gpt-5.5", "", "", "", "", 2, func(message []byte) error {
						writes = append(writes, append([]byte(nil), message...))
						return nil
					})
				if tt.eventType == "response.failed" {
					require.NoError(t, err)
					require.NotNil(t, result)
					require.Equal(t, 9, result.Usage.InputTokens)
				} else {
					require.ErrorContains(t, err, tt.message)
				}
				require.NotEmpty(t, writes)
				last := writes[len(writes)-1]
				messagePath := "error.message"
				if tt.eventType != "" {
					messagePath = "response.error.message"
					require.Equal(t, "response.failed", gjson.GetBytes(last, "type").String())
					require.True(t, gjson.GetBytes(last, "sequence_number").Exists())
					require.Positive(t, gjson.GetBytes(last, "response.created_at").Int())
				}
				wantMessage := tt.wantMessage
				if configured != "" {
					wantMessage = configured
				}
				require.Equal(t, wantMessage, gjson.GetBytes(last, messagePath).String())
				require.NotContains(t, string(last), "Excel BPS")
				require.NotContains(t, string(last), "private backend")
				opsErr, ok := GetOpsStreamError(c)
				require.True(t, ok)
				require.Equal(t, tt.message, opsErr.Message)
			})
		}
	}
}

func TestPassthroughLifecycle_WrapsInternalUpstreamFailure(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, configured := range []string{"", openAIWSCustomFailureMessage} {
		t.Run(fmt.Sprintf("configured_%v", configured != ""), func(t *testing.T) {
			controlCtx, cancelControl := context.WithCancelCause(WithUpstreamErrorMessage(context.Background(), configured))
			defer cancelControl(context.Canceled)
			upstream := newStagedPassthroughConn()
			upstream.Send(`{"type":"response.failed","response":{"id":"resp_internal_failure","error":{"code":"server_error","message":"stream disconnected before completion: Excel BPS did not deliver validated output within the allowed interval","details":"private backend stack trace"},"usage":{"input_tokens":9,"output_tokens":2}}}`)
			svc := newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream)
			turns := make(chan *OpenAIForwardResult, 1)
			contexts := make(chan *gin.Context, 1)
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx, svc, passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
				contexts <- c
				return &OpenAIWSIngressHooks{AfterTurn: func(_ int, result *OpenAIForwardResult, _ error) {
					turns <- result
				}}
			})
			defer server.Close()
			clientConn := dialPassthroughLifecycleClient(t, server)
			defer func() { _ = clientConn.CloseNow() }()
			failed, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
			require.NoError(t, err)
			wantMessage := configured
			if wantMessage == "" {
				wantMessage = OpenAIUpstreamFailureClientMessage
			}
			require.Equal(t, wantMessage, gjson.GetBytes(failed, "response.error.message").String())
			require.NotContains(t, string(failed), "Excel BPS")
			require.NotContains(t, string(failed), "private backend")
			select {
			case result := <-turns:
				require.NotNil(t, result)
				require.False(t, result.SucceededForScheduling())
				require.Equal(t, 9, result.Usage.InputTokens)
				require.Equal(t, 2, result.Usage.OutputTokens)
			case <-time.After(3 * time.Second):
				t.Fatal("failed passthrough turn did not complete")
			}
			require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("passthrough did not exit")
			}
			ops, ok := GetOpsStreamError(<-contexts)
			require.True(t, ok)
			require.Equal(t, openAIWSInternalFailureExample, ops.Message)
		})
	}
}

func TestPassthroughLifecycle_ConfiguredUpstreamCloseMessage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, configured := range []string{openAIWSCustomFailureMessage, strings.Repeat("请稍后重试。", 30)} {
		t.Run(fmt.Sprintf("bytes_%d", len(configured)), func(t *testing.T) {
			controlCtx, cancel := context.WithCancelCause(WithUpstreamErrorMessage(context.Background(), configured))
			defer cancel(context.Canceled)
			upstream := newStagedPassthroughConn()
			upstream.Fail(NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "upstream handshake rejected", errors.New(openAIWSInternalFailureExample)))
			server, serverErr := startPassthroughLifecycleServer(t, controlCtx,
				newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream), passthroughLifecycleAccount())
			defer server.Close()
			clientConn := dialPassthroughLifecycleClient(t, server)
			defer func() { _ = clientConn.CloseNow() }()
			if len(configured) > 120 {
				message, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
				require.NoError(t, err)
				require.Equal(t, configured, gjson.GetBytes(message, "error.message").String())
			}
			_, err := readPassthroughLifecycleFrame(t, clientConn, 3*time.Second)
			var closeErr coderws.CloseError
			require.ErrorAs(t, err, &closeErr)
			require.Equal(t, coderws.StatusPolicyViolation, closeErr.Code)
			require.Equal(t, truncateString(configured, 120), closeErr.Reason)
			require.True(t, utf8.ValidString(closeErr.Reason))
			require.LessOrEqual(t, len(closeErr.Reason), 120)
			select {
			case serverErr := <-serverErr:
				require.ErrorContains(t, serverErr, openAIWSInternalFailureExample)
			case <-time.After(3 * time.Second):
				t.Fatal("passthrough close did not complete")
			}
		})
	}
}

func TestPassthroughLifecycle_ConfiguredBinaryError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, eventType := range []string{"error", "response.failed"} {
		t.Run(eventType, func(t *testing.T) {
			controlCtx, cancel := context.WithCancelCause(WithUpstreamErrorMessage(context.Background(), openAIWSCustomFailureMessage))
			defer cancel(context.Canceled)
			upstream := newStagedPassthroughConn()
			unchangedPayload := []byte{0x00, 0x01, 0xff, 0xfe}
			upstream.frames <- stagedPassthroughFrame{messageType: coderws.MessageBinary, payload: unchangedPayload}
			failurePayload := []byte(`{"type":"error","error":{"code":"cyber_policy","message":"Request blocked by content policy"}}`)
			messagePath := "error.message"
			if eventType == "response.failed" {
				failurePayload = []byte(`{"type":"response.failed","response":{"id":"resp_binary","error":{"code":"cyber_policy","message":"Request blocked by content policy"}}}`)
				messagePath = "response.error.message"
			}
			upstream.frames <- stagedPassthroughFrame{messageType: coderws.MessageBinary, payload: failurePayload}
			contexts := make(chan *gin.Context, 1)
			server, serverErr := startPassthroughLifecycleServerWithHooks(t, controlCtx,
				newPassthroughLifecycleService(passthroughLifecycleConfig(), upstream), passthroughLifecycleAccount(), func(c *gin.Context) *OpenAIWSIngressHooks {
					contexts <- c
					return nil
				})
			defer server.Close()
			clientConn := dialPassthroughLifecycleClient(t, server)
			defer func() { _ = clientConn.CloseNow() }()
			readCtx, cancelRead := context.WithTimeout(context.Background(), 3*time.Second)
			msgType, message, err := clientConn.Read(readCtx)
			cancelRead()
			require.NoError(t, err)
			require.Equal(t, coderws.MessageBinary, msgType)
			require.Equal(t, unchangedPayload, message, "opaque binary frames must remain byte-for-byte unchanged")
			readCtx, cancelRead = context.WithTimeout(context.Background(), 3*time.Second)
			msgType, message, err = clientConn.Read(readCtx)
			cancelRead()
			require.NoError(t, err)
			require.Equal(t, coderws.MessageBinary, msgType)
			require.Equal(t, openAIWSCustomFailureMessage, gjson.GetBytes(message, messagePath).String())
			require.NotContains(t, string(message), "Request blocked")
			require.NoError(t, clientConn.Close(coderws.StatusNormalClosure, "done"))
			select {
			case <-serverErr:
			case <-time.After(3 * time.Second):
				t.Fatal("passthrough did not exit")
			}
			ops, ok := GetOpsStreamError(<-contexts)
			require.True(t, ok)
			require.Equal(t, "Request blocked by content policy", ops.Message)
			require.Equal(t, "cyber_policy", ops.Code)
		})
	}
}
