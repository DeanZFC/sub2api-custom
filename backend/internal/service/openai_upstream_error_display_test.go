package service

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/model"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const internalExcelFailure = "stream disconnected before completion: Excel BPS did not deliver validated output within the allowed interval"

func TestConfiguredUpstreamErrorRetainsProtocolAndUsage(t *testing.T) {
	message := "服务忙，请重试。\n联系客服：\"站内帮助\""
	ctx := WithUpstreamErrorMessage(context.Background(), message)
	for _, code := range []string{"invalid_parameter", "context_length_exceeded", "content_policy_violation", "insufficient_quota", "server_error"} {
		t.Run(code, func(t *testing.T) {
			original, err := json.Marshal(gin.H{
				"type": "response.failed", "sequence_number": 3, "details": internalExcelFailure,
				"response": gin.H{"id": "resp_test", "created_at": 123, "status": "failed", "output": []any{},
					"error": gin.H{"code": code, "message": internalExcelFailure, "details": internalExcelFailure},
					"usage": gin.H{"input_tokens": 17, "output_tokens": 3, "input_tokens_details": gin.H{"cached_tokens": 10}, "debug": internalExcelFailure}},
			})
			require.NoError(t, err)
			before := string(original)
			client, changed := sanitizeOpenAIResponseFailedEventForClient(original, "response.failed", true, ctx)
			require.True(t, changed)
			require.NotContains(t, string(client), internalExcelFailure)
			require.Equal(t, message, gjson.GetBytes(client, "response.error.message").String())
			require.Equal(t, code, gjson.GetBytes(client, "response.error.code").String())
			require.Equal(t, int64(17), gjson.GetBytes(client, "response.usage.input_tokens").Int())
			require.Equal(t, int64(10), gjson.GetBytes(client, "response.usage.input_tokens_details.cached_tokens").Int())
			require.Equal(t, before, string(original))
			requireDecodableOpenAIFailedTerminal(t, "event: response.failed\ndata: "+string(client)+"\n\n")
		})
	}
}

func TestOpenAIUpstreamErrorClientMessage(t *testing.T) {
	for _, tc := range []struct {
		name, payload, message, want string
	}{
		{"internal bare", `{"type":"error"}`, internalExcelFailure, OpenAIUpstreamFailureClientMessage},
		{"server failure", `{"error":{"type":"server_error"}}`, "worker shard failed with exception at internal.node:42", OpenAIUpstreamFailureClientMessage},
		{"misclassified invalid request", `{"error":{"type":"invalid_request_error"}}`, internalExcelFailure, OpenAIUpstreamFailureClientMessage},
		{"request parameter", `{"error":{"type":"invalid_request_error","param":"tools[0].name"}}`, "Missing required parameter: tools[0].name", "Missing required parameter: tools[0].name"},
		{"context window", `{"response":{"error":{"code":"context_length_exceeded"}}}`, "input exceeds the context window", "input exceeds the context window"},
		{"policy", `{"error":{"code":"content_policy_violation"}}`, "blocked by our content policy", "blocked by our content policy"},
		{"billing", `{"error":{"code":"insufficient_quota"}}`, "provider balance empty", UpstreamBillingExhaustedClientMessage},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, OpenAIUpstreamErrorClientMessage([]byte(tc.payload), tc.message))
		})
	}
}

func TestOpenAIInternalFailureEventHidesRepeatedDetails(t *testing.T) {
	for _, eventType := range []string{"error", "response.failed"} {
		t.Run(eventType, func(t *testing.T) {
			original, err := json.Marshal(gin.H{
				"type": eventType, "sequence_number": 27, "message": internalExcelFailure, "details": internalExcelFailure,
				"error": gin.H{"type": "server_error", "code": "server_error", "message": internalExcelFailure, "stack": internalExcelFailure},
				"response": gin.H{"id": "resp_original", "object": "response", "created_at": 1789656300, "model": "gpt-test", "status": "failed",
					"error": gin.H{"code": "server_error", "message": internalExcelFailure}, "metadata": gin.H{"debug": internalExcelFailure}},
			})
			require.NoError(t, err)
			before := string(original)
			client, changed := sanitizeOpenAIResponseFailedEventForClient(original, eventType, true)
			require.True(t, changed)
			require.NotContains(t, string(client), "Excel BPS")
			require.Contains(t, string(client), OpenAIUpstreamFailureClientMessage)
			require.Equal(t, before, string(original), "original remains available for accounting and diagnostics")
			require.Equal(t, int64(27), gjson.GetBytes(client, "sequence_number").Int())
			require.Equal(t, "resp_original", gjson.GetBytes(client, "response.id").String())
			if eventType == "response.failed" {
				requireDecodableOpenAIFailedTerminal(t, "event: response.failed\ndata: "+string(client)+"\n\n")
			} else {
				require.Equal(t, OpenAIUpstreamFailureClientMessage, gjson.GetBytes(client, "message").String())
			}
		})
	}
}

func TestOpenAIInternalFailureResponsesDelivery(t *testing.T) {
	for _, mode := range []string{"native", "passthrough"} {
		for _, tc := range []struct {
			name, eventType   string
			partial, failover bool
		}{
			{"bare before output", "error", false, false},
			{"bare after output", "error", true, false},
			{"failed before output", "response.failed", false, true},
			{"failed after output", "response.failed", true, false},
		} {
			t.Run(mode+"/"+tc.name, func(t *testing.T) {
				c, rec := newNonStreamingFailoverContext(t)
				payload, err := json.Marshal(gin.H{
					"type": tc.eventType, "sequence_number": 2,
					"error": gin.H{"type": "server_error", "code": "server_error", "message": internalExcelFailure},
					"response": gin.H{"id": "resp_test", "object": "response", "created_at": 1789656300, "status": "failed", "output": []any{},
						"usage": gin.H{"input_tokens": 7}, "error": gin.H{"code": "server_error", "message": internalExcelFailure}},
				})
				require.NoError(t, err)
				stream := ""
				if tc.partial {
					stream = "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n"
				}
				stream += "event: " + tc.eventType + "\ndata: " + string(payload) + "\n\n"
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"X-Request-Id": {"rid_internal_error"}}, Body: io.NopCloser(strings.NewReader(stream))}
				svc := &OpenAIGatewayService{cfg: &config.Config{}}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Name: "test"}
				if mode == "native" {
					_, err = svc.handleStreamingResponse(c.Request.Context(), resp, c, account, time.Now(), "gpt-test", "gpt-test")
				} else {
					_, err = svc.handleStreamingResponsePassthrough(c.Request.Context(), resp, c, account, time.Now(), "gpt-test", "gpt-test")
				}
				require.Error(t, err)
				var failover *UpstreamFailoverError
				require.Equal(t, tc.failover, errors.As(err, &failover))
				if tc.failover {
					require.Empty(t, rec.Body.String())
					require.Contains(t, string(failover.ResponseBody), internalExcelFailure)
					return
				}
				require.Contains(t, err.Error(), internalExcelFailure)
				require.NotContains(t, rec.Body.String(), "Excel BPS")
				require.Contains(t, rec.Body.String(), OpenAIUpstreamFailureClientMessage)
				requireDecodableOpenAIFailedTerminal(t, rec.Body.String())
				if tc.partial {
					require.Contains(t, rec.Body.String(), "partial answer")
				}
				if tc.eventType == "response.failed" {
					require.Equal(t, internalExcelFailure, c.GetString(OpsUpstreamErrorMessageKey))
				}
			})
		}
	}
}

func TestOpenAIInternalFailureNonStreamingKeepsDiagnostics(t *testing.T) {
	for _, passthrough := range []bool{false, true} {
		c, rec := newNonStreamingFailoverContext(t)
		svc := newNonStreamingFailoverService()
		payload, err := json.Marshal(gin.H{"type": "error", "error": gin.H{"message": internalExcelFailure}})
		require.NoError(t, err)
		body := sseTerminalBody("error", string(payload))
		if passthrough {
			_, err = svc.handlePassthroughSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
		} else {
			_, err = svc.handleSSEToJSON(newNonStreamingSSEResponse(), c, newNonStreamingFailoverAccount(), body, "model", "model")
		}
		require.ErrorContains(t, err, internalExcelFailure)
		require.Equal(t, http.StatusBadGateway, rec.Code)
		require.Equal(t, OpenAIUpstreamFailureClientMessage, gjson.GetBytes(rec.Body.Bytes(), "error.message").String())
		require.Equal(t, internalExcelFailure, c.GetString(OpsUpstreamErrorMessageKey))
	}
}

func TestOpenAIInternalFailurePassthroughRuleDisplay(t *testing.T) {
	for _, custom := range []bool{false, true} {
		rec := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(rec)
		rule := newNonFailoverPassthroughRule(http.StatusBadGateway, "Excel BPS", http.StatusBadGateway, "请稍后再试或联系管理员。")
		rule.PassthroughBody = !custom
		svc := &ErrorPassthroughService{}
		svc.setLocalCache([]*model.ErrorPassthroughRule{rule})
		BindErrorPassthroughService(c, svc)
		payload, err := json.Marshal(gin.H{"error": gin.H{"message": internalExcelFailure}})
		require.NoError(t, err)
		_, _, message, matched := applyErrorPassthroughRule(c, PlatformOpenAI, http.StatusBadGateway, payload, 502, "upstream_error", "failure")
		require.True(t, matched)
		if custom {
			require.Equal(t, *rule.CustomMessage, message)
		} else {
			require.Equal(t, OpenAIUpstreamFailureClientMessage, message)
		}
	}
}
