package service

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func billingRetryTestResponse() *http.Response {
	return &http.Response{
		StatusCode: http.StatusForbidden,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body: io.NopCloser(strings.NewReader(
			`{"error":{"code":"insufficient_balance","message":"Provider account has no credits remaining"}}`,
		)),
	}
}

func TestAnthropicPassthroughBilling403SkipsSameAccountRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{billingRetryTestResponse()}}
	repo := &openAIStream403AccountRepo{}
	svc := &GatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	account := newAnthropicAPIKeyAccountForTest()
	account.Credentials["custom_error_codes_enabled"] = true
	account.Credentials["custom_error_codes"] = []any{float64(http.StatusInternalServerError)}
	require.True(t, svc.shouldRetryUpstreamError(account, http.StatusForbidden))

	requestBody := []byte(`{"model":"claude-3-5-sonnet-latest","messages":[{"role":"user","content":"hello"}]}`)
	_, err := svc.forwardAnthropicAPIKeyPassthrough(
		context.Background(), c, account, requestBody, "claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest", false, time.Now(),
	)

	var failoverErr *UpstreamFailoverError
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, 1, upstream.callCount)
	require.True(t, failoverErr.IsUpstreamBillingExhausted())
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
	require.Equal(t, UpstreamBillingExhaustedClientMessage, failoverErr.ClientMessage)
	require.Zero(t, repo.setErrorCalls, "custom error-code policy still controls persistent account state")
	require.False(t, IsResponseCommitted(c))
	require.Empty(t, rec.Body.String())
}

func TestBedrockBilling403SkipsSameAccountRetry(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)

	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{billingRetryTestResponse()}}
	repo := &openAIStream403AccountRepo{}
	svc := &GatewayService{
		cfg:              &config.Config{},
		httpUpstream:     upstream,
		rateLimitService: &RateLimitService{accountRepo: repo},
	}
	// Bedrock accounts currently use a distinct account type; this exercises
	// the executor's OAuth retry policy if it is selected by a future caller.
	account := &Account{ID: 202, Platform: PlatformAnthropic, Type: AccountTypeOAuth}
	require.True(t, svc.shouldRetryUpstreamError(account, http.StatusForbidden))

	resp, err := svc.executeBedrockUpstream(
		context.Background(), c, account, []byte(`{"messages":[]}`),
		"anthropic.claude-3-5-sonnet-20241022-v2:0", "us-east-1", false,
		NewBedrockSigner("access", "secret", "", "us-east-1"), "", "",
	)
	require.NoError(t, err)
	result, err := svc.handleBedrockUpstreamErrors(context.Background(), resp, c, account)

	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.ErrorAs(t, err, &failoverErr)
	require.Equal(t, 1, upstream.callCount)
	require.True(t, failoverErr.IsUpstreamBillingExhausted())
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
	require.Equal(t, UpstreamBillingExhaustedClientMessage, failoverErr.ClientMessage)
	require.Equal(t, 1, repo.setErrorCalls)
	require.False(t, IsResponseCommitted(c))
	require.Empty(t, rec.Body.String())
}

func TestAnthropicPassthroughHTTP200BillingEnvelopeFailsOverBeforeWriting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	const billingBody = `{"type":"error","error":{"type":"invalid_request_error","message":"Credit balance is too low"}}`
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Body:       io.NopCloser(strings.NewReader(billingBody)),
	}}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream, rateLimitService: &RateLimitService{accountRepo: &openAIStream403AccountRepo{}}}
	result, err := svc.forwardAnthropicAPIKeyPassthrough(context.Background(), c, newAnthropicAPIKeyAccountForTest(),
		[]byte(`{"model":"claude-3-5-sonnet-latest","messages":[]}`), "claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest", false, time.Now())
	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsUpstreamBillingExhausted())
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Equal(t, http.StatusBadGateway, failoverErr.ClientStatusCode)
	require.Empty(t, rec.Body.String())
	require.False(t, IsResponseCommitted(c))
}

func TestAnthropicPassthroughSSEBillingBeforeOutputFailsOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	const billingBody = `{"type":"error","error":{"code":"insufficient_balance","message":"Upstream account balance exhausted"}}`
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader("event: error\ndata: " + billingBody + "\n\n")),
	}}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream, rateLimitService: &RateLimitService{accountRepo: &openAIStream403AccountRepo{}}}
	result, err := svc.forwardAnthropicAPIKeyPassthrough(context.Background(), c, newAnthropicAPIKeyAccountForTest(),
		[]byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[]}`), "claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest", true, time.Now())
	var failoverErr *UpstreamFailoverError
	require.Nil(t, result)
	require.ErrorAs(t, err, &failoverErr)
	require.True(t, failoverErr.IsUpstreamBillingExhausted())
	require.False(t, failoverErr.RetryableOnSameAccount)
	require.Empty(t, rec.Body.String())
	require.False(t, c.Writer.Written())
	require.Empty(t, c.Writer.Header().Get("Content-Type"), "next account must choose its own response type")
}

func TestAnthropicPassthroughSSEBillingAfterOutputHidesProviderBalance(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", nil)
	const billingBody = `{"type":"error","error":{"code":"insufficient_balance","message":"Upstream account balance exhausted"}}`
	stream := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":11}}}\n\n" +
		"event: error\ndata: " + billingBody + "\n\n"
	upstream := &queuedHTTPUpstreamStub{responses: []*http.Response{{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body:       io.NopCloser(strings.NewReader(stream)),
	}}}
	svc := &GatewayService{cfg: &config.Config{}, httpUpstream: upstream, rateLimitService: &RateLimitService{accountRepo: &openAIStream403AccountRepo{}}}
	result, err := svc.forwardAnthropicAPIKeyPassthrough(context.Background(), c, newAnthropicAPIKeyAccountForTest(),
		[]byte(`{"model":"claude-3-5-sonnet-latest","stream":true,"messages":[]}`), "claude-3-5-sonnet-latest", "claude-3-5-sonnet-latest", true, time.Now())
	var failoverErr *UpstreamFailoverError
	require.Error(t, err)
	require.NotErrorAs(t, err, &failoverErr)
	require.NotNil(t, result)
	require.Equal(t, 11, result.Usage.InputTokens)
	require.Contains(t, rec.Body.String(), "message_start")
	require.NotContains(t, rec.Body.String(), "insufficient_balance")
	require.NotContains(t, rec.Body.String(), "Upstream account balance exhausted")
}
