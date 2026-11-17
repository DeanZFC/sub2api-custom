//go:build unit

package service

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func ccValidationSSE(payloads ...string) string {
	var body strings.Builder
	for _, payload := range payloads {
		body.WriteString("data: " + payload + "\n\n")
	}
	return body.String()
}

func runCCStreamValidationForward(t *testing.T, protocol string, upstreamBody io.ReadCloser, configure func(*gin.Context)) (*OpenAIForwardResult, error, *gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	body := []byte(`{"model":"deepseek-v4.1-flash","input":"hello","stream":true}`)
	account := forceChatResponsesFallbackAccount()
	if protocol == "messages" {
		body = []byte(`{"model":"deepseek-v4.1-flash","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
		account = forceChatMessagesFallbackAccount()
	}
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/"+protocol, bytes.NewReader(body))
	c.Request.Header.Set("Content-Type", "application/json")
	if configure != nil {
		configure(c)
	}
	upstream := &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": {"text/event-stream"}, "X-Request-Id": {"cc-validation-upstream"}},
		Body:       upstreamBody,
	}}
	svc := &OpenAIGatewayService{cfg: rawChatCompletionsTestConfig(), httpUpstream: upstream}
	var result *OpenAIForwardResult
	var err error
	if protocol == "responses" {
		result, err = svc.Forward(c.Request.Context(), c, account, body)
	} else {
		result, err = svc.ForwardAsAnthropic(c.Request.Context(), c, account, body, "", "")
	}
	return result, err, c, rec
}

// Exercise the complete conversion entry points: accounting-only or malformed
// upstream streams must not be turned into successful client terminal events.
func TestCCFallbackStreamRejectsIncompleteOrEmptyResponses(t *testing.T) {
	textChunk := `{"id":"chat_partial","choices":[{"index":0,"delta":{"content":"partial answer"},"finish_reason":null}]}`
	usageChunk := `{"choices":[],"usage":{"prompt_tokens":94682,"completion_tokens":0,"total_tokens":94682}}`
	errorWithUsage := `{"error":{"type":"server_error","message":"provider failed"},"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`
	for _, protocol := range []string{"responses", "messages"} {
		for _, tc := range []struct {
			name             string
			stream           string
			code             string
			input, output    int
			wantsFirstOutput bool
		}{
			{"partial text EOF", ccValidationSSE(textChunk), "upstream_stream_truncated", 0, 0, true},
			{"usage does not terminate generation", ccValidationSSE(textChunk, usageChunk), "upstream_stream_truncated", 94682, 0, true},
			{"metered empty response", ccValidationSSE(usageChunk, "[DONE]"), "upstream_empty_response", 94682, 0, false},
			{"done only", ccValidationSSE("[DONE]"), "upstream_empty_response", 0, 0, false},
			{"empty deltas", ccValidationSSE(`{"choices":[{"index":0,"delta":{"role":"assistant","content":"","reasoning_content":""},"finish_reason":"stop"}]}`, "[DONE]"), "upstream_empty_response", 0, 0, false},
			{"error only", ccValidationSSE(errorWithUsage, "[DONE]"), "upstream_stream_error", 12, 3, false},
			{"error after output", ccValidationSSE(textChunk, errorWithUsage, "[DONE]"), "upstream_stream_error", 12, 3, true},
			{"named error event", "event: error\ndata: {\"message\":\"provider failed\"}\n\n", "upstream_stream_error", 0, 0, false},
			{"failed event type", ccValidationSSE(`{"type":"response.failed","response":{"error":{"message":"provider failed"}}}`), "upstream_stream_error", 0, 0, false},
			{"invalid JSON after output", ccValidationSSE(textChunk, `{"choices":[`, "[DONE]"), "upstream_invalid_stream", 0, 0, true},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				result, err, c, rec := runCCStreamValidationForward(t, protocol, io.NopCloser(strings.NewReader(tc.stream)), nil)
				require.Error(t, err)
				code, message, classified := OpenAIUpstreamStreamReadErrorDetails(err)
				require.True(t, classified, "handler needs a typed stream error to terminate the client protocol")
				require.Equal(t, tc.code, code)
				require.NotEmpty(t, message)
				require.NotNil(t, result, "failed streams must retain already reported usage")
				require.Equal(t, tc.input, result.Usage.InputTokens)
				require.Equal(t, tc.output, result.Usage.OutputTokens)
				require.False(t, result.Usage.CacheReadKnown)
				if tc.wantsFirstOutput {
					require.NotNil(t, result.FirstTokenMs)
					require.Contains(t, rec.Body.String(), "partial answer")
				} else {
					require.Nil(t, result.FirstTokenMs, "metadata and errors are not generated output")
				}
				require.NotContains(t, rec.Body.String(), "event: response.completed")
				require.NotContains(t, rec.Body.String(), "event: message_stop")
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
				events, ok := c.Get(OpsUpstreamErrorsKey)
				require.True(t, ok, "HTTP 200 stream failures must remain observable in Ops")
				require.NotEmpty(t, events)
			})
		}
	}
}

func TestCCFallbackStreamPreservesCompatibleCompletions(t *testing.T) {
	for _, protocol := range []string{"responses", "messages"} {
		for _, tc := range []struct {
			name, stream, visible string
			wantsFirstOutput      bool
		}{
			{"text and DONE", ccValidationSSE(`{"choices":[{"index":0,"delta":{"content":"hello"}}]}`, "[DONE]"), "hello", true},
			{"finish without DONE", ccValidationSSE(`{"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}`), "hello", true},
			{"reasoning only", ccValidationSSE(`{"choices":[{"index":0,"delta":{"reasoning_content":"thinking"},"finish_reason":"stop"}]}`, "[DONE]"), "thinking", true},
			{"reasoning alias", ccValidationSSE(`{"choices":[{"index":0,"delta":{"reasoning":"thinking"},"finish_reason":"stop"}]}`, "[DONE]"), "thinking", true},
			{"tool only", ccValidationSSE(`{"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_test","type":"function","function":{"name":"lookup","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`, "[DONE]"), "lookup", true},
			{"null error field", ccValidationSSE(`{"error":null,"choices":[{"index":0,"delta":{"content":"hello"},"finish_reason":"stop"}]}`, "[DONE]"), "hello", true},
			{"explicit output limit", ccValidationSSE(`{"choices":[{"index":0,"delta":{},"finish_reason":"length"}]}`, "[DONE]"), "", false},
			{"explicit content filter", ccValidationSSE(`{"choices":[{"index":0,"delta":{},"finish_reason":"content_filter"}]}`, "[DONE]"), "", false},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				result, err, c, rec := runCCStreamValidationForward(t, protocol, io.NopCloser(strings.NewReader(tc.stream)), nil)
				require.NoError(t, err)
				require.NotNil(t, result)
				if tc.visible != "" {
					require.Contains(t, rec.Body.String(), tc.visible)
				}
				if tc.wantsFirstOutput {
					require.NotNil(t, result.FirstTokenMs)
				} else {
					require.Nil(t, result.FirstTokenMs)
				}
				terminal := "event: response.completed"
				if protocol == "messages" {
					terminal = "event: message_stop"
				}
				require.Equal(t, 1, strings.Count(rec.Body.String(), terminal))
				if tc.name == "explicit output limit" {
					if protocol == "responses" {
						require.Contains(t, rec.Body.String(), `"status":"incomplete"`)
					} else {
						require.Contains(t, rec.Body.String(), `"stop_reason":"max_tokens"`)
					}
				}
				_, hasUpstreamError := c.Get(OpsUpstreamErrorsKey)
				require.False(t, hasUpstreamError)
			})
		}
	}
}

type ccValidationCancelReader struct {
	io.Reader
	cancel context.CancelFunc
}

func (r *ccValidationCancelReader) Read(p []byte) (int, error) {
	if r.cancel != nil {
		r.cancel()
		r.cancel = nil
	}
	return r.Reader.Read(p)
}

func (*ccValidationCancelReader) Close() error { return nil }

func TestCCFallbackStreamClientDisconnectRetainsUsageWithoutUpstreamFailure(t *testing.T) {
	// No finish or DONE on purpose: once the client has gone away the same EOF
	// must not be attributed to the provider, and upstream billing must survive.
	stream := ccValidationSSE(
		`{"choices":[{"index":0,"delta":{"content":"partial answer"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`,
	)
	for _, protocol := range []string{"responses", "messages"} {
		for _, mode := range []string{"request canceled", "downstream write failed"} {
			t.Run(protocol+"/"+mode, func(t *testing.T) {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				var reader io.ReadCloser = io.NopCloser(strings.NewReader(stream))
				if mode == "request canceled" {
					reader = &ccValidationCancelReader{Reader: strings.NewReader(stream), cancel: cancel}
				}
				result, err, c, rec := runCCStreamValidationForward(t, protocol, reader, func(c *gin.Context) {
					c.Request = c.Request.WithContext(ctx)
					if mode == "downstream write failed" {
						c.Writer = &openAIRawStreamDisconnectedWriter{ResponseWriter: c.Writer}
					}
				})
				require.NotNil(t, result)
				require.True(t, result.ClientDisconnect)
				require.Equal(t, 12, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				_, _, classified := OpenAIUpstreamStreamReadErrorDetails(err)
				require.False(t, classified, "client departure is not an upstream stream failure")
				_, hasUpstreamError := c.Get(OpsUpstreamErrorsKey)
				require.False(t, hasUpstreamError)
				require.NotContains(t, rec.Body.String(), "event: response.completed")
				require.NotContains(t, rec.Body.String(), "event: message_stop")
			})
		}
	}
}

func TestCCFallbackStreamUpstreamReadFailureWhileClientActive(t *testing.T) {
	stream := ccValidationSSE(
		`{"choices":[{"index":0,"delta":{"content":"partial answer"}}]}`,
		`{"choices":[],"usage":{"prompt_tokens":12,"completion_tokens":3,"total_tokens":15}}`,
	)
	for _, protocol := range []string{"responses", "messages"} {
		for _, tc := range []struct {
			name string
			err  error
		}{
			{"unexpected EOF", io.ErrUnexpectedEOF},
			{"upstream deadline", context.DeadlineExceeded},
			{"upstream context canceled", context.Canceled},
		} {
			t.Run(protocol+"/"+tc.name, func(t *testing.T) {
				reader := &errTailReader{data: []byte(stream), err: tc.err}
				result, err, c, rec := runCCStreamValidationForward(t, protocol, reader, nil)
				require.NoError(t, c.Request.Context().Err(), "the client has not canceled its request")
				require.ErrorIs(t, err, tc.err)
				code, message, classified := OpenAIUpstreamStreamReadErrorDetails(err)
				require.True(t, classified, "an upstream timeout must not be mistaken for client departure")
				require.Equal(t, OpenAIUpstreamStreamReadErrorCode, code)
				require.NotEmpty(t, message)
				require.NotNil(t, result)
				require.False(t, result.ClientDisconnect)
				require.Equal(t, 12, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				require.False(t, result.Usage.CacheReadKnown)
				require.NotNil(t, result.FirstTokenMs)
				require.Contains(t, rec.Body.String(), "partial answer")
				require.NotContains(t, rec.Body.String(), "event: response.completed")
				require.NotContains(t, rec.Body.String(), "event: message_stop")
				require.NotContains(t, rec.Body.String(), "data: [DONE]")
				events, ok := c.Get(OpsUpstreamErrorsKey)
				require.True(t, ok)
				require.NotEmpty(t, events)
			})
		}
	}
}
