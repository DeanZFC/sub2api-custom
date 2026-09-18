package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAnthropicBillingJSONSuccessEnvelopeFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	repo := &openAIStream403AccountRepo{}
	svc := &GatewayService{cfg: &config.Config{}, rateLimitService: &RateLimitService{accountRepo: repo}}
	account := newAnthropicAPIKeyAccountForTest()
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     make(http.Header),
		Body:       io.NopCloser(strings.NewReader(`{"type":"error","error":{"type":"invalid_request_error","message":"Your credit balance is too low to access the Anthropic API."}}`)),
	}
	_, err := svc.handleNonStreamingResponse(context.Background(), resp, c, account, "claude", "claude")
	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsUpstreamBillingExhausted())
	require.Equal(t, 1, repo.setErrorCalls)
	require.False(t, c.Writer.Written())
	require.Empty(t, rec.Body.String())
}

func TestAnthropicBillingSSEPreservesFailoverAndPartialUsage(t *testing.T) {
	for _, started := range []bool{false, true} {
		name := "before output"
		prefix := ""
		if started {
			name = "after output"
			prefix = "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"msg_billing\",\"type\":\"message\",\"role\":\"assistant\",\"usage\":{\"input_tokens\":11}}}\n\n"
		}
		t.Run(name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
			body := []byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[{"role":"user","content":"hello"}]}`)
			parsed, err := ParseGatewayRequest(NewRequestBodyRef(body), PlatformAnthropic)
			require.NoError(t, err)
			repo := &openAIStream403AccountRepo{}
			cfg := &config.Config{Gateway: config.GatewayConfig{MaxLineSize: defaultMaxLineSize}}
			svc := &GatewayService{
				cfg: cfg, rateLimitService: &RateLimitService{accountRepo: repo}, deferredService: &DeferredService{},
				httpUpstream: &anthropicHTTPUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"text/event-stream"}},
					Body: io.NopCloser(strings.NewReader(prefix + "event: error\ndata: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_balance\",\"message\":\"provider balance is zero\"}}\n\n")),
				}},
			}
			result, err := svc.Forward(context.Background(), c, newAnthropicOAuthAccountForPartialUsageTest(), parsed)
			require.Error(t, err)
			require.NotContains(t, rec.Body.String(), "provider balance")
			require.NotContains(t, rec.Body.String(), "insufficient_balance")
			if started {
				require.NotNil(t, result)
				require.Equal(t, 11, result.Usage.InputTokens)
				_, isFailover := err.(*UpstreamFailoverError)
				require.False(t, isFailover)
			} else {
				require.Nil(t, result)
				var failover *UpstreamFailoverError
				require.ErrorAs(t, err, &failover)
				require.True(t, failover.IsUpstreamBillingExhausted())
				require.Empty(t, rec.Body.String())
				require.Empty(t, c.Writer.Header().Get("Content-Type"))
			}
		})
	}
}
