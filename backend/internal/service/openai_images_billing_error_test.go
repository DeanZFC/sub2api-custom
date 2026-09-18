package service

import (
	"bytes"
	"context"
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
)

func TestOpenAIImagesHTTPBillingErrorBypassesPassthrough(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	rules := &ErrorPassthroughService{}
	rules.setLocalCache([]*model.ErrorPassthroughRule{
		newNonFailoverPassthroughRule(http.StatusBadRequest, "insufficient balance", http.StatusTeapot, "balance: $18"),
	})
	BindErrorPassthroughService(c, rules)

	account := &Account{ID: 86002, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
	resp := &http.Response{
		StatusCode: http.StatusBadRequest,
		Header:     http.Header{"X-Request-Id": []string{"image-billing-http"}},
		Body:       io.NopCloser(strings.NewReader(`{"error":{"code":"insufficient_balance","message":"insufficient balance: $18"}}`)),
	}
	result, err := (&OpenAIGatewayService{}).handleOpenAIImagesErrorResponse(context.Background(), resp, c, account, "gpt-image-1")

	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.True(t, errors.As(err, &failover))
	require.True(t, failover.IsUpstreamBillingExhausted())
	require.Equal(t, http.StatusBadGateway, failover.ClientStatusCode)
	require.Equal(t, UpstreamBillingExhaustedClientMessage, failover.ClientMessage)
	require.Equal(t, "image-billing-http", failover.ResponseHeaders.Get("x-request-id"))
	require.False(t, c.Writer.Written())
	require.Empty(t, recorder.Body.String())
}

func TestOpenAIImagesSemanticBillingFailsOverBeforeClientOutput(t *testing.T) {
	tests := []struct {
		name        string
		accountType string
		stream      bool
		upstream    string
	}{
		{
			name:        "API key non-stream JSON error",
			accountType: AccountTypeAPIKey,
			upstream:    `{"error":{"code":"insufficient_quota","message":"credit balance is insufficient: $18"}}`,
		},
		{
			name:        "OAuth non-stream response.failed",
			accountType: AccountTypeOAuth,
			upstream:    "data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"credit balance is insufficient: $18\"}}}\n\n",
		},
		{
			name:        "OAuth stream error",
			accountType: AccountTypeOAuth,
			stream:      true,
			upstream: "data: {\"type\":\"response.created\",\"response\":{\"created_at\":1710000020}}\n\n" +
				"data: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"credit balance is insufficient: $18\"}}\n\n",
		},
		{
			name:        "API key SSE event with separate event line",
			accountType: AccountTypeAPIKey,
			stream:      true,
			upstream: "event: error\n" +
				"data: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"credit balance is insufficient: $18\"}}\n\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			requestBody := `{"model":"gpt-image-1","prompt":"draw a cat","response_format":"b64_json"`
			if tt.stream {
				requestBody += `,"stream":true`
			}
			requestBody += `}`
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewBufferString(requestBody))
			c.Request.Header.Set("Content-Type", "application/json")
			contentType := "application/json"
			if tt.stream {
				contentType = "text/event-stream"
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{contentType}},
				Body:       io.NopCloser(strings.NewReader(tt.upstream)),
			}}}
			parsed, err := svc.ParseOpenAIImagesRequest(c, []byte(requestBody))
			require.NoError(t, err)
			credential := map[string]any{"api_key": "test-key"}
			if tt.accountType == AccountTypeOAuth {
				credential = map[string]any{"access_token": "test-token"}
			}
			account := &Account{ID: 86003, Platform: PlatformOpenAI, Type: tt.accountType, Credentials: credential}
			result, err := svc.ForwardImages(context.Background(), c, account, []byte(requestBody), parsed, "")

			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.True(t, errors.As(err, &failover), "%v", err)
			require.True(t, failover.IsUpstreamBillingExhausted())
			require.Equal(t, http.StatusBadGateway, failover.ClientStatusCode)
			require.Equal(t, UpstreamBillingExhaustedClientMessage, failover.ClientMessage)
			require.False(t, failover.RetryableOnSameAccount)
			require.False(t, c.Writer.Written())
			require.Empty(t, recorder.Body.String())
			require.NotContains(t, c.Writer.Header().Get("Content-Type"), "event-stream")
		})
	}
}

func TestOpenAIImagesBillingSSEAfterOutputUsesGenericError(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
	resp := &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"image_generation.partial_image\",\"partial_image_b64\":\"cGFydA==\"}\n\n" +
				"event: error\n" +
				"data: {\"type\":\"error\",\"error\":{\"code\":\"insufficient_quota\",\"message\":\"credit balance is insufficient: $18\"}}\n\n" +
				"data: {\"type\":\"image_generation.completed\",\"b64_json\":\"must-not-be-written\"}\n\n",
		)),
	}

	_, _, _, _, err := (&OpenAIGatewayService{}).handleOpenAIImagesStreamingResponse(resp, c, time.Now(), nil)
	require.Error(t, err)
	require.Contains(t, recorder.Body.String(), "cGFydA==")
	require.Contains(t, recorder.Body.String(), UpstreamBillingExhaustedClientMessage)
	require.Equal(t, 1, strings.Count(recorder.Body.String(), UpstreamBillingExhaustedClientMessage))
	require.NotContains(t, recorder.Body.String(), "insufficient_quota")
	require.NotContains(t, recorder.Body.String(), "$18")
	require.NotContains(t, recorder.Body.String(), "must-not-be-written")
}

func TestOpenAIImagesOAuthBillingAfterPartialOutputDoesNotFailOver(t *testing.T) {
	gin.SetMode(gin.TestMode)
	requestBody := []byte(`{"model":"gpt-image-1","prompt":"draw a cat","stream":true,"response_format":"b64_json"}`)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", bytes.NewReader(requestBody))
	c.Request.Header.Set("Content-Type", "application/json")
	svc := &OpenAIGatewayService{cfg: &config.Config{}, httpUpstream: &httpUpstreamRecorder{resp: &http.Response{
		StatusCode: http.StatusOK,
		Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
		Body: io.NopCloser(strings.NewReader(
			"data: {\"type\":\"response.image_generation_call.partial_image\",\"partial_image_b64\":\"cGFydA==\",\"partial_image_index\":0}\n\n" +
				"data: {\"type\":\"response.failed\",\"response\":{\"error\":{\"code\":\"insufficient_quota\",\"message\":\"credit balance is insufficient: $18\"}}}\n\n" +
				"data: {\"type\":\"response.completed\",\"response\":{\"output\":[{\"type\":\"image_generation_call\",\"result\":\"must-not-be-written\"}]}}\n\n",
		)),
	}}}
	parsed, err := svc.ParseOpenAIImagesRequest(c, requestBody)
	require.NoError(t, err)
	account := &Account{ID: 86004, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"access_token": "test-token"}}
	result, err := svc.ForwardImages(context.Background(), c, account, requestBody, parsed, "")

	require.Nil(t, result)
	var failover *UpstreamFailoverError
	require.False(t, errors.As(err, &failover))
	var upstreamErr *OpenAIImagesUpstreamError
	require.True(t, errors.As(err, &upstreamErr))
	require.True(t, c.Writer.Written())
	require.Contains(t, recorder.Body.String(), "cGFydA==")
	require.Equal(t, 1, strings.Count(recorder.Body.String(), UpstreamBillingExhaustedClientMessage))
	require.NotContains(t, recorder.Body.String(), "insufficient_quota")
	require.NotContains(t, recorder.Body.String(), "$18")
	require.NotContains(t, recorder.Body.String(), "must-not-be-written")
}

func TestOpenAIImagesStreamEventBufferBounded(t *testing.T) {
	for _, upstreamBody := range []string{
		"data: " + strings.Repeat("x", 256) + "\n\n",
		strings.Repeat("event: ping\n", 24) + "\n",
	} {
		t.Run(upstreamBody[:10], func(t *testing.T) {
			gin.SetMode(gin.TestMode)
			recorder := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(recorder)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/images/generations", nil)
			resp := &http.Response{
				StatusCode: http.StatusOK,
				Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
				Body:       io.NopCloser(strings.NewReader(upstreamBody)),
			}
			svc := &OpenAIGatewayService{cfg: &config.Config{Gateway: config.GatewayConfig{UpstreamResponseReadMaxBytes: 128}}}
			_, _, _, _, err := svc.handleOpenAIImagesStreamingResponse(resp, c, time.Now(), nil)
			require.ErrorIs(t, err, ErrUpstreamResponseBodyTooLarge)
			require.Empty(t, recorder.Body.String())
		})
	}
}
