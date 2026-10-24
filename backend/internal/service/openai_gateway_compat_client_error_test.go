//go:build unit

package service

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

const compatInternalFailureFixture = "stream disconnected before completion: Excel BPS did not deliver validated output within the allowed interval"

func TestOpenAICompatClientErrorWrapsInternalFailureAndRetainsDiagnostics(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, protocol := range []string{"chat", "messages"} {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%t", protocol, stream), func(t *testing.T) {
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, nil)
				// Compatible providers may mislabel an internal failure as invalid request.
				sse := "data: " + fmt.Sprintf(`{"type":"response.failed","response":{"id":"resp_failed","object":"response","status":"failed","output":[],"error":{"type":"invalid_request_error","code":"invalid_request_error","message":%q}}}`, compatInternalFailureFixture) + "\n\n"
				if stream {
					sse = "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_test\",\"object\":\"response\",\"model\":\"gpt-5.4\",\"status\":\"in_progress\",\"output\":[]}}\n\n" +
						"data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial\"}\n\n" + sse
				}
				resp := &http.Response{StatusCode: http.StatusOK, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(sse))}
				svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
				account := rawChatCompletionsTestAccount()
				var err error
				if protocol == "chat" {
					if stream {
						_, err = svc.handleChatStreamingResponse(resp, c, account, "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now(), 0)
					} else {
						_, err = svc.handleChatBufferedStreamingResponse(resp, c, account, "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now())
					}
				} else if stream {
					_, err = svc.handleAnthropicStreamingResponse(resp, c, account, "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now())
				} else {
					_, err = svc.handleAnthropicBufferedStreamingResponse(resp, c, account, "gpt-5.4", "gpt-5.4", "gpt-5.4", time.Now())
				}
				require.ErrorContains(t, err, compatInternalFailureFixture)
				require.Contains(t, rec.Body.String(), OpenAIUpstreamFailureClientMessage)
				require.NotContains(t, rec.Body.String(), "Excel BPS")
				require.Equal(t, compatInternalFailureFixture, c.GetString(OpsUpstreamErrorMessageKey))
				if stream {
					require.Contains(t, rec.Body.String(), "partial")
					require.Equal(t, http.StatusOK, rec.Code)
				} else {
					require.Equal(t, http.StatusBadGateway, rec.Code)
				}
			})
		}
	}
}

func TestRawChatClientErrorWrapsInternalFailureAndRetainsUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, stream := range []bool{false, true} {
		t.Run(fmt.Sprintf("stream=%t", stream), func(t *testing.T) {
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/chat/completions", nil)
			payload := fmt.Sprintf(`{"error":{"type":"server_error","code":"internal_error","message":%q,"details":"private provider diagnostic"},"debug":"private provider diagnostic","usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}}`, compatInternalFailureFixture)
			upstreamBody := payload
			if stream {
				upstreamBody = "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"partial\"}}]}\n\n" + "data: " + payload + "\n\ndata: [DONE]\n\n"
			}
			resp := &http.Response{StatusCode: http.StatusOK, Header: make(http.Header), Body: io.NopCloser(strings.NewReader(upstreamBody))}
			svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig()}
			account := rawChatCompletionsTestAccount()
			var result *OpenAIForwardResult
			var err error
			if stream {
				result, err = svc.streamRawChatCompletions(c, resp, account, "gpt-5.4", "gpt-5.4", "gpt-5.4", nil, nil, time.Now(), 0)
			} else {
				result, err = svc.bufferRawChatCompletions(c, resp, account, "gpt-5.4", "gpt-5.4", "gpt-5.4", nil, nil, time.Now())
			}
			require.NoError(t, err, "display wrapping must not change raw forwarding retry semantics")
			require.NotNil(t, result)
			require.Equal(t, 7, result.Usage.InputTokens)
			require.Equal(t, 2, result.Usage.OutputTokens)
			require.Equal(t, compatInternalFailureFixture, c.GetString(OpsUpstreamErrorMessageKey))
			require.Contains(t, rec.Body.String(), OpenAIUpstreamFailureClientMessage)
			require.NotContains(t, rec.Body.String(), "Excel BPS")
			require.NotContains(t, rec.Body.String(), "private provider diagnostic")
			clientError := rec.Body.String()
			if stream {
				require.Contains(t, clientError, "partial")
				require.Contains(t, clientError, "data: [DONE]")
				for _, line := range strings.Split(clientError, "\n") {
					if data, ok := extractOpenAISSEDataLine(line); ok && gjson.Get(data, "error").Exists() {
						clientError = data
						break
					}
				}
			}
			require.Equal(t, "server_error", gjson.Get(clientError, "error.type").String())
			require.Equal(t, "internal_error", gjson.Get(clientError, "error.code").String())
			require.Equal(t, int64(7), gjson.Get(clientError, "usage.prompt_tokens").Int())
		})
	}
}

func TestRawChatClientErrorPreservesActionableErrorsAndOrdinaryContent(t *testing.T) {
	requestError := []byte(`{"error":{"type":"invalid_request_error","code":"invalid_parameter","param":"temperature","message":"temperature must be between 0 and 2"}}`)
	require.True(t, isRawChatCompletionsErrorPayload(requestError))
	require.Equal(t, string(requestError), string(rawChatCompletionsClientErrorBody(requestError)))
	content := []byte(`{"choices":[{"index":0,"delta":{"content":"Excel BPS is mentioned in this answer"}}]}`)
	require.False(t, isRawChatCompletionsErrorPayload(content))
	require.False(t, isRawChatCompletionsErrorPayload([]byte(`{"error":null,"choices":[]}`)))
}
