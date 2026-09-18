//go:build unit

package service

import (
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

const geminiStreamBillingError = `{"error":{"code":"insufficient_balance","message":"provider account balance exhausted"},"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":9,"totalTokenCount":14}}`

func geminiBillingStreamResponse(body string) *http.Response {
	return &http.Response{
		StatusCode: http.StatusOK,
		Header: http.Header{
			"Content-Type":                   []string{"text/event-stream"},
			"X-RateLimit-Remaining-Requests": []string{"0"},
			"X-RateLimit-Remaining-Tokens":   []string{"0"},
			"X-RateLimit-Reset-Requests":     []string{"1h"},
			"X-RateLimit-Reset-Tokens":       []string{"1h"},
		},
		Body: io.NopCloser(strings.NewReader(body)),
	}
}

func assertGeminiBillingFailoverBeforeWrite(t *testing.T, c *gin.Context, body string, result any, err error) {
	t.Helper()
	require.Nil(t, result)
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsUpstreamBillingExhausted())
	require.Empty(t, body, "billing failover must not emit downstream bytes")
	require.False(t, c.Writer.Written(), "billing failover must leave the response writable for the next account")
	require.Empty(t, c.Writer.Header().Get("Content-Type"), "failed attempt must not pin the final error to SSE")
	require.Empty(t, c.Writer.Header().Get("X-RateLimit-Remaining-Requests"), "failed account headers must not leak into the final response")
}

func TestGeminiMessagesBillingStreamFailsOverBeforeWritingHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	svc := &GeminiMessagesCompatService{}

	result, err := svc.handleStreamingResponse(
		c,
		geminiBillingStreamResponse("data: "+geminiStreamBillingError+"\n\n"),
		time.Now(),
		"claude-test",
		geminiBillingResponseContext{account: geminiSignalTestAccount(), requestID: "req-billing", model: "gemini-test"},
	)

	assertGeminiBillingFailoverBeforeWrite(t, c, rec.Body.String(), result, err)
}

func TestGeminiMessagesBillingAfterOutputReturnsSanitizedErrorAndTerminalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	svc := &GeminiMessagesCompatService{}
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}` + "\n\n" +
		"data: " + geminiStreamBillingError + "\n\n"

	result, err := svc.handleStreamingResponse(
		c,
		geminiBillingStreamResponse(upstream),
		time.Now(),
		"claude-test",
		geminiBillingResponseContext{account: geminiSignalTestAccount(), requestID: "req-billing", model: "gemini-test"},
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 5, result.usage.InputTokens)
	require.Equal(t, 9, result.usage.OutputTokens, "terminal billing frame usage must be retained")
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, "text/event-stream", c.Writer.Header().Get("Content-Type"))
	out := rec.Body.String()
	require.Contains(t, out, `"text":"partial"`)
	require.Contains(t, out, "event: error")
	require.Contains(t, out, UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, out, "provider account balance exhausted")
	require.NotContains(t, out, "insufficient_balance")
}

func TestGeminiChatCompletionsBillingStreamFailsOverBeforeWritingHeaders(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	svc := &GeminiMessagesCompatService{
		cfg:                  &config.Config{},
		responseHeaderFilter: compileResponseHeaderFilter(&config.Config{}),
	}

	result, err := svc.handleChatCompletionsStreamingResponseFromGemini(
		c.Request.Context(),
		c,
		geminiBillingStreamResponse("data: "+geminiStreamBillingError+"\n\n"),
		time.Now(),
		"gemini-test",
		geminiSignalTestAccount(),
		"req-billing",
		"gemini-test",
		false,
		true,
	)

	assertGeminiBillingFailoverBeforeWrite(t, c, rec.Body.String(), result, err)
}

func TestGeminiChatCompletionsBillingAfterOutputReturnsSanitizedErrorAndTerminalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	svc := &GeminiMessagesCompatService{}
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}` + "\n\n" +
		"data: " + geminiStreamBillingError + "\n\n"

	result, err := svc.handleChatCompletionsStreamingResponseFromGemini(
		c.Request.Context(),
		c,
		geminiBillingStreamResponse(upstream),
		time.Now(),
		"gemini-test",
		geminiSignalTestAccount(),
		"req-billing",
		"gemini-test",
		false,
		true,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 5, result.usage.InputTokens)
	require.Equal(t, 9, result.usage.OutputTokens, "terminal billing frame usage must be retained")
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, "text/event-stream", c.Writer.Header().Get("Content-Type"))
	out := rec.Body.String()
	require.Contains(t, out, `"content":"partial"`)
	require.Contains(t, out, UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, out, "provider account balance exhausted")
	require.NotContains(t, out, "insufficient_balance")
	require.NotContains(t, out, "data: [DONE]", "an errored stream must not be finalized as successful")
}

func TestGeminiChatCompletionsBillingAfterExistingDownstreamBytesDoesNotFailOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	c.Header("Content-Type", "text/event-stream")
	_, err := io.WriteString(c.Writer, ": keepalive\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	svc := &GeminiMessagesCompatService{}

	result, err := svc.handleChatCompletionsStreamingResponseFromGemini(
		c.Request.Context(),
		c,
		geminiBillingStreamResponse("data: "+geminiStreamBillingError+"\n\n"),
		time.Now(),
		"gemini-test",
		geminiSignalTestAccount(),
		"req-billing",
		"gemini-test",
		false,
		true,
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.usage.OutputTokens)
	require.True(t, IsResponseCommitted(c))
	require.Contains(t, rec.Body.String(), UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, rec.Body.String(), "provider account balance exhausted")
}

func TestGeminiNativeBillingAfterInitialControlFrameStillFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, prefix := range []string{"data:\n\n", "data: [DONE]\n\n"} {
		t.Run(strings.TrimSpace(prefix), func(t *testing.T) {
			c, rec := newGeminiNativeTestContext(t)
			svc := &GeminiMessagesCompatService{
				cfg:                  &config.Config{},
				responseHeaderFilter: compileResponseHeaderFilter(&config.Config{}),
			}
			resp := geminiBillingStreamResponse(prefix + "data: " + geminiStreamBillingError + "\n\n")

			result, err := svc.handleNativeStreamingResponse(c, resp, time.Now(), false, geminiSignalTestAccount(), "req-billing", "gemini-test")

			assertGeminiBillingFailoverBeforeWrite(t, c, rec.Body.String(), result, err)
		})
	}
}

func TestGeminiNativeBillingAfterOutputReturnsSanitizedErrorAndTerminalUsage(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	svc := &GeminiMessagesCompatService{}
	upstream := `data: {"candidates":[{"content":{"parts":[{"text":"partial"}]}}],"usageMetadata":{"promptTokenCount":5,"candidatesTokenCount":2,"totalTokenCount":7}}` + "\n\n" +
		"data: " + geminiStreamBillingError + "\n\n"

	result, err := svc.handleNativeStreamingResponse(
		c,
		geminiBillingStreamResponse(upstream),
		time.Now(),
		false,
		geminiSignalTestAccount(),
		"req-billing",
		"gemini-test",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 5, result.usage.InputTokens)
	require.Equal(t, 9, result.usage.OutputTokens)
	require.True(t, IsResponseCommitted(c))
	require.Equal(t, "text/event-stream", c.Writer.Header().Get("Content-Type"))
	out := rec.Body.String()
	require.Contains(t, out, `"text":"partial"`)
	require.Contains(t, out, UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, out, "provider account balance exhausted")
	require.NotContains(t, out, "insufficient_balance")
}

func TestGeminiNativeBillingAfterExistingDownstreamBytesDoesNotFailOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, rec := newGeminiNativeTestContext(t)
	c.Header("Content-Type", "text/event-stream")
	_, err := io.WriteString(c.Writer, ": keepalive\n\n")
	require.NoError(t, err)
	c.Writer.Flush()
	svc := &GeminiMessagesCompatService{}

	result, err := svc.handleNativeStreamingResponse(
		c,
		geminiBillingStreamResponse("data: "+geminiStreamBillingError+"\n\n"),
		time.Now(),
		false,
		geminiSignalTestAccount(),
		"req-billing",
		"gemini-test",
	)

	require.NoError(t, err)
	require.NotNil(t, result)
	require.Equal(t, 9, result.usage.OutputTokens)
	require.True(t, IsResponseCommitted(c))
	require.Contains(t, rec.Body.String(), UpstreamBillingExhaustedClientMessage)
	require.NotContains(t, rec.Body.String(), "provider account balance exhausted")
}
