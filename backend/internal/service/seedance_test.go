//go:build unit

package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func seedanceTestAccount() *Account {
	return &Account{ID: 9, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Credentials: map[string]any{
		"api_key": "ark-secret", "base_url": "https://ark.cn-beijing.volces.com/api/v3",
		"openai_capabilities": []string{"seedance"},
		"model_mapping":       map[string]any{"video": "ep-seedance"},
	}}
}

func TestSeedanceNativeForwarding(t *testing.T) {
	body := []byte(`{"model":"video","content":[{"type":"text","text":"waves"},{"type":"image_url","image_url":{"url":"https://example.com/first.png"},"role":"first_frame"},{"type":"audio_url","audio_url":{"url":"https://example.com/audio.mp3"}}],"duration":-1,"generate_audio":true,"future_field":{"keep":true}}`)
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"id":"task-1"}`)}
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
	result, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointCreate, "", body)
	require.NoError(t, err)
	require.JSONEq(t, `{"id":"task-1"}`, w.Body.String())
	require.Equal(t, "seedance:task-1", result.ResponseID)
	require.Zero(t, result.Usage.OutputTokens)
	require.Equal(t, "video", result.BillingModel)
	require.Equal(t, "ep-seedance", result.UpstreamModel)
	require.Equal(t, "https://ark.cn-beijing.volces.com/api/v3/contents/generations/tasks", upstream.request.URL.String())
	require.Equal(t, "Bearer ark-secret", upstream.request.Header.Get("Authorization"))
	forwarded, err := io.ReadAll(upstream.request.Body)
	require.NoError(t, err)
	require.Equal(t, "ep-seedance", gjson.GetBytes(forwarded, "model").String())
	for _, field := range []string{"content", "duration", "generate_audio", "future_field"} {
		require.Equal(t, gjson.GetBytes(body, field).Raw, gjson.GetBytes(forwarded, field).Raw)
	}
}

func TestSeedanceStatusAndDelete(t *testing.T) {
	for _, status := range []string{"queued", "running", "failed", "cancelled", "expired", "succeeded"} {
		t.Run(status, func(t *testing.T) {
			body := `{"id":"task-1","status":"` + status + `","model":"ep-seedance","content":{"video_url":"https://cdn.example/video.mp4"},"usage":{"completion_tokens":12345}}`
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(body)}
			svc := &OpenAIGatewayService{httpUpstream: upstream}
			c, w := grokMediaContentTestContext(http.MethodGet, "/api/v3/contents/generations/tasks/task-1", nil)
			result, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointStatus, "seedance:task-1", nil)
			require.NoError(t, err)
			require.JSONEq(t, body, w.Body.String())
			require.Equal(t, "/api/v3/contents/generations/tasks/task-1", upstream.request.URL.Path)
			if status == "succeeded" {
				require.Equal(t, 12345, result.Usage.OutputTokens)
			} else {
				require.Zero(t, result.Usage.OutputTokens)
			}
			require.Zero(t, result.VideoCount, "must use token billing, not Grok seconds")
		})
	}
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse("")}
	upstream.response.StatusCode = http.StatusNoContent
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodDelete, "/api/v3/contents/generations/tasks/task-1", nil)
	_, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointDelete, "seedance:task-1", nil)
	require.NoError(t, err)
	require.Equal(t, http.MethodDelete, upstream.request.Method)
	require.Equal(t, http.StatusNoContent, w.Code)
}

func TestSeedanceValidationAndCapability(t *testing.T) {
	for _, body := range []string{`{`, `[]`, `{}`, `{"model":12,"content":[{}]}`, `{"model":"x","content":[]}`} {
		_, err := ParseSeedanceRequest([]byte(body))
		require.Error(t, err)
	}
	info, err := ParseSeedanceRequest([]byte(`{"model":"x","content":[{"type":"text","text":"first"},{"type":"text","text":"second"},{"type":"image_url","image_url":{"url":"https://example.com/a.png"}}]}`))
	require.NoError(t, err)
	require.Contains(t, string(info.ModerationBody()), "first")
	require.Contains(t, string(info.ModerationBody()), "second")
	require.Contains(t, string(info.ModerationBody()), "https://example.com/a.png")
	for _, id := range []string{"", "..", "a/b", "a?b", "a#b", "%2e%2e"} {
		_, err := buildSeedanceURL("https://example.com", SeedanceEndpointStatus, id)
		require.Error(t, err, id)
	}
	for _, base := range []string{"https://example.com", "https://example.com/api/v3/", "https://example.com/v3"} {
		url, err := buildSeedanceURL(base, SeedanceEndpointCreate, "")
		require.NoError(t, err)
		require.NotContains(t, url, "/v3/api/v3")
	}
	a := seedanceTestAccount()
	require.True(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
	a.Type = AccountTypeOAuth
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
	a.Type = AccountTypeAPIKey
	delete(a.Credentials, "openai_capabilities")
	require.False(t, a.SupportsOpenAIEndpointCapability(OpenAIEndpointCapabilitySeedance))
}

func TestSeedancePreservesUpstreamErrorsWithoutRetry(t *testing.T) {
	upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(`{"error":{"code":"QuotaExceeded","message":"quota exhausted"}}`)}
	upstream.response.StatusCode = 429
	svc := &OpenAIGatewayService{httpUpstream: upstream}
	c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
	_, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), SeedanceEndpointCreate, "", []byte(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
	require.Error(t, err)
	require.Equal(t, 429, w.Code)
	require.Contains(t, w.Body.String(), "QuotaExceeded")
	require.Len(t, upstream.requests, 1)
}

func TestSeedanceBillingErrorsArePrivateWithoutRetry(t *testing.T) {
	for _, tc := range []struct {
		name     string
		status   int
		endpoint GrokMediaEndpoint
		body     string
	}{
		{"payment_required", http.StatusPaymentRequired, SeedanceEndpointCreate, `{"error":{"code":"PaymentRequired","message":"operator balance exhausted"}}`},
		{"forbidden_balance", http.StatusForbidden, SeedanceEndpointCreate, `{"error":{"code":"insufficient_balance","message":"余额不足"}}`},
		{"quota", http.StatusTooManyRequests, SeedanceEndpointCreate, `{"error":{"code":"insufficient_quota","message":"check your plan and billing details"}}`},
		{"semantic_error", http.StatusOK, SeedanceEndpointCreate, `{"error":{"code":"insufficient_balance","message":"余额不足"}}`},
		{"failed_task", http.StatusOK, SeedanceEndpointStatus, `{"id":"task-1","status":"failed","error":{"code":"insufficient_balance","message":"余额不足","details":{"balance":0}}}`},
		{"nested_error", http.StatusOK, SeedanceEndpointStatus, `{"id":"task-1","status":"failed","response":{"error":{"code":"insufficient_balance","message":"余额不足"}}}`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			upstream := &grokMediaContentUpstreamStub{response: grokMediaContentStatusResponse(tc.body)}
			upstream.response.StatusCode = tc.status
			upstream.response.Header.Set("X-Upstream-Error", "insufficient_balance")
			repo := &rateLimitAccountRepoStub{}
			svc := &OpenAIGatewayService{
				httpUpstream:     upstream,
				rateLimitService: NewRateLimitService(repo, nil, &config.Config{}, nil, nil),
			}
			c, w := grokMediaContentTestContext(http.MethodPost, "/api/v3/contents/generations/tasks", nil)
			result, err := svc.ForwardSeedance(context.Background(), c, seedanceTestAccount(), tc.endpoint, "seedance:task-1", []byte(`{"model":"video","content":[{"type":"text","text":"waves"}]}`))
			require.Error(t, err)
			require.Nil(t, result)
			var failover *UpstreamFailoverError
			require.False(t, errors.As(err, &failover), "ambiguous asynchronous creates must never retry")
			require.Len(t, upstream.requests, 1)
			require.True(t, IsResponseCommitted(c))
			require.Equal(t, UpstreamBillingExhaustedClientMessage, gjson.Get(w.Body.String(), "error.message").String())
			require.NotContains(t, w.Body.String(), "insufficient_balance")
			require.NotContains(t, w.Body.String(), "余额不足")
			require.NotContains(t, w.Body.String(), "billing details")
			require.Empty(t, w.Header().Get("X-Upstream-Error"))
			require.NotEmpty(t, c.GetString(OpsUpstreamErrorMessageKey), "operator diagnostics should retain the failure")
			require.Equal(t, 1, repo.setErrorCalls, "stop scheduling the depleted provider account")
			if tc.endpoint == SeedanceEndpointStatus {
				require.Equal(t, http.StatusOK, w.Code)
				require.Equal(t, "task-1", gjson.Get(w.Body.String(), "id").String())
				require.Equal(t, "failed", gjson.Get(w.Body.String(), "status").String())
				require.False(t, gjson.Get(w.Body.String(), "error.details").Exists())
			} else {
				require.Equal(t, http.StatusBadGateway, w.Code)
			}
		})
	}
}
