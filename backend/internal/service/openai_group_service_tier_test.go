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

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"github.com/tidwall/gjson"
)

func groupUltrafastContext() context.Context {
	return context.WithValue(context.Background(), ctxkey.Group, &Group{
		ID: 7, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true,
		ForceOpenAIServiceTier: OpenAIFastTierUltrafast,
	})
}

func TestGroupForceUltrafastPolicyHTTPAndWS(t *testing.T) {
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, policy := range []struct {
		action string
		want   string
	}{
		{BetaPolicyActionPass, OpenAIFastTierUltrafast},
		{BetaPolicyActionFilter, ""},
		{BetaPolicyActionBlock, ""},
		{OpenAIFastPolicyActionForcePriority, OpenAIFastTierPriority},
	} {
		t.Run(policy.action, func(t *testing.T) {
			svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{
				Rules: []OpenAIFastPolicyRule{{
					ServiceTier: OpenAIFastTierUltrafast, Action: policy.action, Scope: BetaPolicyScopeAll,
				}},
			})
			for _, incoming := range []string{"", "default", "priority", "flex", "ultrafast"} {
				t.Run("incoming="+incoming, func(t *testing.T) {
					body := `{"type":"response.create","model":"gpt-6-astra","input":"hello"`
					if incoming != "" {
						body += `,"service_tier":"` + incoming + `"`
					}
					body += `}`
					outHTTP, err := svc.applyOpenAIFastPolicyToBody(groupUltrafastContext(), account, "gpt-6-astra", []byte(body))
					outWS, blocked, wsErr := svc.applyOpenAIFastPolicyToWSResponseCreate(groupUltrafastContext(), account, "gpt-6-astra", []byte(body))
					require.NoError(t, wsErr)
					if policy.action == BetaPolicyActionBlock {
						var httpBlocked *OpenAIFastBlockedError
						require.ErrorAs(t, err, &httpBlocked)
						require.NotNil(t, blocked)
						return
					}
					require.NoError(t, err)
					require.Nil(t, blocked)
					for _, out := range [][]byte{outHTTP, outWS} {
						require.Equal(t, policy.want, gjson.GetBytes(out, "service_tier").String())
						require.Equal(t, "hello", gjson.GetBytes(out, "input").String())
					}
				})
			}
		})
	}
}

func TestGroupForceUltrafastRequiresTrustedOpenAIGroup(t *testing.T) {
	for _, tt := range []struct {
		name            string
		groupPlatform   string
		accountPlatform string
		hydrated        bool
		want            string
	}{
		{"openai", PlatformOpenAI, PlatformOpenAI, true, OpenAIFastTierUltrafast},
		{"composite", PlatformComposite, PlatformOpenAI, true, OpenAIFastTierUltrafast},
		{"non-openai account", PlatformComposite, PlatformGrok, true, ""},
		{"non-openai group", PlatformAnthropic, PlatformOpenAI, true, ""},
		{"unhydrated", PlatformOpenAI, PlatformOpenAI, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{
				ID: 7, Platform: tt.groupPlatform, Status: StatusActive, Hydrated: tt.hydrated,
				ForceOpenAIServiceTier: OpenAIFastTierUltrafast,
				// The explicit tier takes precedence over legacy snapshots with both set.
				ForceOpenAIFast: true,
			})
			require.Equal(t, tt.want, openAIGroupForcedServiceTier(ctx, &Account{Platform: tt.accountPlatform}))
		})
	}
}

func TestGroupForceUltrafastDoesNotRewriteOtherWSFrames(t *testing.T) {
	svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
	account := &Account{Platform: PlatformOpenAI, Type: AccountTypeOAuth}
	for _, frame := range []string{
		`{"type":"response.cancel","service_tier":"priority"}`,
		`{"type":"session.update","session":{"model":"gpt-6-astra"}}`,
	} {
		out, blocked, err := svc.applyOpenAIFastPolicyToWSResponseCreate(groupUltrafastContext(), account, "gpt-6-astra", []byte(frame))
		require.NoError(t, err)
		require.Nil(t, blocked)
		require.Equal(t, frame, string(out))
	}
}

func TestForward_GroupForceUltrafast(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, tt := range []struct {
		name        string
		accountType string
		passthrough bool
	}{
		{"api-key", AccountTypeAPIKey, false},
		{"oauth-legacy", AccountTypeOAuth, false},
		{"oauth-passthrough", AccountTypeOAuth, true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","input":"hello","instructions":"test","stream":false,"service_tier":"priority"}`)
			c, _ := gin.CreateTestContext(httptest.NewRecorder())
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/responses", bytes.NewReader(body))
			c.Request.Header.Set("Content-Type", "application/json")
			c.Request.Header.Set("User-Agent", "codex_cli_rs/0.144.1")
			responseJSON := `{"id":"resp_tier","object":"response","status":"completed","model":"gpt-6-astra","service_tier":"ultrafast","output":[],"usage":{"input_tokens":1,"output_tokens":1}}`
			contentType := "application/json"
			if tt.accountType == AccountTypeOAuth {
				responseJSON = "data: {\"type\":\"response.completed\",\"response\":" + responseJSON + "}\n\ndata: [DONE]\n\n"
				contentType = "text/event-stream"
			}
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{contentType}},
				Body: io.NopCloser(strings.NewReader(responseJSON)),
			}}
			svc := newOpenAIGatewayServiceWithSettings(t, DefaultOpenAIFastPolicySettings())
			svc.cfg = &config.Config{}
			svc.httpUpstream = upstream
			account := &Account{
				ID: 123, Name: "tier-test", Platform: PlatformOpenAI, Type: tt.accountType, Concurrency: 1,
				Credentials: map[string]any{"api_key": "sk-test", "access_token": "oauth-token", "chatgpt_account_id": "chatgpt-test"},
				Extra: map[string]any{
					"openai_responses_supported": true, "openai_passthrough": tt.passthrough,
					"openai_oauth_responses_websockets_v2_mode": OpenAIWSIngressModeOff,
				},
				Status: StatusActive, Schedulable: true, RateMultiplier: f64p(1),
			}
			result, err := svc.Forward(groupUltrafastContext(), c, account, body)
			require.NoError(t, err)
			require.NotNil(t, result)
			require.Equal(t, OpenAIFastTierUltrafast, gjson.GetBytes(upstream.lastBody, "service_tier").String())
			require.NotNil(t, result.ServiceTier)
			require.Equal(t, OpenAIFastTierUltrafast, *result.ServiceTier)
			if tt.accountType == AccountTypeOAuth {
				require.Equal(t, "model=gpt-6-astra;tier=ultrafast", upstream.lastReq.Header.Get(openAICodexRoutingHintHeader))
			}
		})
	}
}

func TestMessagesChatFallback_GroupForceUltrafast(t *testing.T) {
	gin.SetMode(gin.TestMode)
	for _, action := range []string{BetaPolicyActionPass, BetaPolicyActionFilter, BetaPolicyActionBlock} {
		t.Run(action, func(t *testing.T) {
			body := []byte(`{"model":"gpt-6-astra","max_tokens":32,"messages":[{"role":"user","content":"hello"}],"stream":false}`)
			rec := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(rec)
			c.Request = httptest.NewRequest(http.MethodPost, "/v1/messages", bytes.NewReader(body))
			upstream := &httpUpstreamRecorder{resp: &http.Response{
				StatusCode: http.StatusOK, Header: http.Header{"Content-Type": []string{"application/json"}},
				Body: io.NopCloser(strings.NewReader(`{"id":"chatcmpl_tier","object":"chat.completion","model":"gpt-6-astra","choices":[{"index":0,"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2}}`)),
			}}
			svc := newOpenAIGatewayServiceWithSettings(t, &OpenAIFastPolicySettings{
				Rules: []OpenAIFastPolicyRule{{ServiceTier: OpenAIFastTierUltrafast, Action: action, Scope: BetaPolicyScopeAll}},
			})
			svc.cfg = rawChatCompletionsTestConfig()
			svc.httpUpstream = upstream
			result, err := svc.ForwardAsAnthropic(groupUltrafastContext(), c, forceChatMessagesFallbackAccount(), body, "", "")
			if action == BetaPolicyActionBlock {
				var blocked *OpenAIFastBlockedError
				require.ErrorAs(t, err, &blocked)
				require.Equal(t, http.StatusForbidden, rec.Code)
				require.Nil(t, upstream.lastReq, "blocked requests must not reach upstream")
				return
			}
			require.NoError(t, err)
			require.NotNil(t, result)
			if action == BetaPolicyActionFilter {
				require.False(t, gjson.GetBytes(upstream.lastBody, "service_tier").Exists())
				require.Nil(t, result.ServiceTier)
			} else {
				require.Equal(t, OpenAIFastTierUltrafast, gjson.GetBytes(upstream.lastBody, "service_tier").String())
				require.NotNil(t, result.ServiceTier)
				require.Equal(t, OpenAIFastTierUltrafast, *result.ServiceTier)
			}
		})
	}
}
