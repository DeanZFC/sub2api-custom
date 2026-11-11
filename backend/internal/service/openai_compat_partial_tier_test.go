package service

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/claude"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func TestOpenAICompatPartialStreamPreservesOutboundTier(t *testing.T) {
	gin.SetMode(gin.TestMode)

	for _, route := range []struct {
		name       string
		anthropic  bool
		tier       string
		filterTier bool
	}{
		{name: "chat_priority", tier: "priority"},
		{name: "chat_ultrafast", tier: "ultrafast"},
		{name: "chat_flex", tier: "flex"},
		{name: "chat_standard"},
		{name: "chat_filtered_priority", tier: "priority", filterTier: true},
		{name: "anthropic_fast", anthropic: true, tier: "priority"},
		{name: "anthropic_standard", anthropic: true},
	} {
		for _, ending := range []string{"eof", "read_error", "failed_event"} {
			t.Run(route.name+"/"+ending, func(t *testing.T) {
				ctx := context.Background()
				path := "/v1/chat/completions"
				body := []byte(fmt.Sprintf(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":true,"service_tier":%q}`, route.tier))
				if route.tier == "" {
					body = []byte(`{"model":"gpt-5.5","messages":[{"role":"user","content":"hello"}],"stream":true}`)
				}
				if route.anthropic {
					path = "/v1/messages"
					body = []byte(`{"model":"gpt-5.5","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":true}`)
				}
				rec := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(rec)
				c.Request = httptest.NewRequest(http.MethodPost, path, bytes.NewReader(body))
				c.Request.Header.Set("Content-Type", "application/json")
				if route.anthropic && route.tier == "priority" {
					c.Request.Header.Set("anthropic-beta", claude.BetaFastMode)
				}

				// A different response tier must neither downgrade nor upgrade the
				// tier from the final, policy-processed outbound request.
				observedTier := "default"
				if route.tier == "" || route.filterTier {
					observedTier = "priority"
				}
				payload := fmt.Sprintf("data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_partial\",\"object\":\"response\",\"model\":\"gpt-5.5\",\"status\":\"in_progress\",\"service_tier\":%q,\"usage\":{\"input_tokens\":19,\"output_tokens\":3}}}\n\n", observedTier) +
					"data: {\"type\":\"response.output_text.delta\",\"output_index\":0,\"content_index\":0,\"delta\":\"partial response\"}\n\n"
				if ending == "failed_event" {
					payload += fmt.Sprintf("data: {\"type\":\"response.failed\",\"response\":{\"id\":\"resp_partial\",\"model\":\"gpt-5.5\",\"status\":\"failed\",\"service_tier\":%q,\"error\":{\"code\":\"server_error\",\"message\":\"partial stream failed\"},\"usage\":{\"input_tokens\":19,\"output_tokens\":3}}}\n\n", observedTier)
				}
				responseBody := io.NopCloser(strings.NewReader(payload))
				if ending == "read_error" {
					responseBody = io.NopCloser(io.MultiReader(strings.NewReader(payload), passthroughErrReadCloser{err: io.ErrUnexpectedEOF}))
				}
				upstream := &httpUpstreamRecorder{resp: &http.Response{
					StatusCode: http.StatusOK,
					Header:     http.Header{"Content-Type": []string{"text/event-stream"}},
					Body:       responseBody,
				}}
				settings := map[string]string{}
				if route.filterTier {
					raw, err := json.Marshal(&OpenAIFastPolicySettings{Rules: []OpenAIFastPolicyRule{{
						ServiceTier: OpenAIFastTierPriority,
						Action:      BetaPolicyActionFilter,
						Scope:       BetaPolicyScopeAll,
					}}})
					require.NoError(t, err)
					settings[SettingKeyOpenAIFastPolicySettings] = string(raw)
				}
				svc := &OpenAIGatewayService{
					cfg:            &config.Config{Security: config.SecurityConfig{URLAllowlist: config.URLAllowlistConfig{Enabled: false}}},
					httpUpstream:   upstream,
					settingService: NewSettingService(&openAIFastPolicyRepoStub{values: settings}, &config.Config{}),
				}
				account := &Account{
					ID: 21, Name: "openai-compatible", Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Concurrency: 1, Credentials: map[string]any{"api_key": "sk-test"},
					Extra: map[string]any{"openai_responses_supported": true}, Status: StatusActive, Schedulable: true,
				}
				var result *OpenAIForwardResult
				var err error
				if route.anthropic {
					result, err = svc.ForwardAsAnthropic(ctx, c, account, body, "partial-tier-session", "gpt-5.5")
				} else {
					result, err = svc.ForwardAsChatCompletions(ctx, c, account, body, "", "gpt-5.5")
				}

				require.Error(t, err)
				require.NotNil(t, result)
				var failoverErr *UpstreamFailoverError
				require.False(t, errors.As(err, &failoverErr), "partial output must retain its original non-failover error")
				switch ending {
				case "eof":
					require.ErrorContains(t, err, "missing terminal event")
				case "read_error":
					require.ErrorIs(t, err, io.ErrUnexpectedEOF)
				case "failed_event":
					require.EqualError(t, err, "upstream response failed: partial stream failed")
				}
				require.Contains(t, rec.Body.String(), "partial response")
				require.NotContains(t, rec.Body.String(), "[DONE]")
				require.Equal(t, 19, result.Usage.InputTokens)
				require.Equal(t, 3, result.Usage.OutputTokens)
				if ending == "failed_event" {
					require.Equal(t, observedTier, result.UpstreamResponseServiceTier)
				} else {
					// Non-terminal Responses frames only echo the requested tier;
					// the observer intentionally does not treat them as execution evidence.
					require.Empty(t, result.UpstreamResponseServiceTier)
				}
				outboundTier := gjson.GetBytes(upstream.lastBody, "service_tier").String()
				if route.tier == "" || route.filterTier {
					require.Empty(t, outboundTier)
					require.Nil(t, result.ServiceTier)
				} else {
					require.Equal(t, route.tier, outboundTier)
					require.NotNil(t, result.ServiceTier)
					require.Equal(t, outboundTier, *result.ServiceTier)
				}
				if route.anthropic {
					// Even a failed terminal event with a response ID must not become
					// a valid continuation merely because its tier is retained.
					require.Empty(t, svc.getOpenAICompatSessionResponseID(ctx, c, account, "partial-tier-session"))
				}
			})
		}
	}
}
