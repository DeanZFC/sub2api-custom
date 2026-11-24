package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// A failing body snapshot proves that eligible requests reach the adapter without
// opening a socket. Disabled routes must not even consume the request body.
func TestGroupSmartWSHonorsTransportSwitches(t *testing.T) {
	for _, tc := range []struct {
		name          string
		disable       func(*OpenAIGatewayService, *Account)
		wantBodyReads int
	}{
		{"enabled", func(*OpenAIGatewayService, *Account) {}, 1},
		{"global_disabled", func(s *OpenAIGatewayService, _ *Account) { s.cfg.Gateway.OpenAIWS.Enabled = false }, 0},
		{"oauth_disabled", func(s *OpenAIGatewayService, _ *Account) { s.cfg.Gateway.OpenAIWS.OAuthEnabled = false }, 0},
		{"v2_disabled", func(s *OpenAIGatewayService, _ *Account) { s.cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = false }, 0},
		{"global_force_http", func(s *OpenAIGatewayService, _ *Account) { s.cfg.Gateway.OpenAIWS.ForceHTTP = true }, 0},
		{"account_force_http", func(_ *OpenAIGatewayService, a *Account) { a.Extra["openai_ws_force_http"] = true }, 0},
		{"account_v2_disabled", func(_ *OpenAIGatewayService, a *Account) {
			a.Extra["openai_oauth_responses_websockets_v2_enabled"] = false
		}, 0},
		{"retired_vm_route_excluded", func(_ *OpenAIGatewayService, a *Account) { a.Extra["openai_vm_bound"] = true }, 0},
		{"group_disabled", func(s *OpenAIGatewayService, _ *Account) {
			s.settingService.groupSmartWSCache[1] = groupSmartWSCacheEntry{enabled: false, expires: time.Now().Add(time.Minute)}
		}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.Enabled = true
			cfg.Gateway.OpenAIWS.OAuthEnabled = true
			cfg.Gateway.OpenAIWS.ResponsesWebsocketsV2 = true
			settings := &SettingService{settingRepo: &smartSettingsStub{}, groupSmartWSCache: map[int64]groupSmartWSCacheEntry{1: {enabled: true, expires: time.Now().Add(time.Minute)}}}
			svc := &OpenAIGatewayService{cfg: cfg, settingService: settings}
			account := &Account{ID: 7, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{"openai_oauth_responses_websockets_v2_enabled": true}}
			tc.disable(svc, account)
			ctx := context.WithValue(context.Background(), ctxkey.Group, &Group{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true})
			ctx = context.WithValue(ctx, ctxkey.UserID, int64(5))
			ctx = context.WithValue(ctx, groupSmartWSAttemptKey{}, &groupSmartWSAttempt{apiKeyID: 9})
			req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://chatgpt.com/backend-api/codex/responses", strings.NewReader("{}"))
			require.NoError(t, err)
			req.Header.Set("Authorization", "Bearer synthetic-token")
			bodyReads := 0
			req.GetBody = func() (io.ReadCloser, error) { bodyReads++; return nil, errors.New("synthetic snapshot error") }
			resp, handled, err := svc.tryGroupSmartWS(req, "", account)
			require.NoError(t, err)
			require.Nil(t, resp)
			require.False(t, handled)
			require.Equal(t, tc.wantBodyReads, bodyReads)
		})
	}
}

func TestSmartWSAttemptSurvivesNestedForwarders(t *testing.T) {
	ctx, attempt := beginGroupSmartWSAttempt(context.Background(), nil)
	attempt.sent.Store(true)
	nested, next := beginGroupSmartWSAttempt(ctx, nil)
	require.Same(t, attempt, next)
	require.True(t, next.sent.Load())
	require.Same(t, attempt, nested.Value(groupSmartWSAttemptKey{}))
}

func TestGroupSmartWSFailureDeliveredBeforeVisibleOutput(t *testing.T) {
	const first = "event: error\ndata: " + `{"type":"error","error":{"code":"server_is_overloaded","message":"capacity"},"usage":{"input_tokens":17}}` + "\n\n"
	const tail = "event: response.failed\ndata: " + `{"type":"response.failed","response":{"id":"resp_test","status":"failed","error":{"code":"server_is_overloaded","message":"capacity"},"usage":{"input_tokens":17}}}` + "\n\n"
	for _, mode := range []string{"normal", "passthrough"} {
		for _, stream := range []struct {
			name, body  string
			inputTokens int
		}{
			{"with_tail", first + tail, 17},
			{"bare_only", first, 17},
			{"unbilled_tail", strings.ReplaceAll(first+tail, `,"usage":{"input_tokens":17}`, ""), 0},
		} {
			t.Run(mode+"/"+stream.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				c, _ := gin.CreateTestContext(w)
				c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
				ctx, attempt := beginGroupSmartWSAttempt(c.Request.Context(), c)
				attempt.sent.Store(true)
				svc := &OpenAIGatewayService{cfg: &config.Config{}, toolCorrector: NewCodexToolCorrector()}
				account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeOAuth}
				resp := &http.Response{StatusCode: 200, Header: http.Header{"Content-Type": {"text/event-stream"}}, Body: io.NopCloser(strings.NewReader(stream.body))}
				var usage *OpenAIUsage
				var err error
				if mode == "normal" {
					result, streamErr := svc.handleStreamingResponse(ctx, resp, c, account, time.Now(), "gpt-test", "gpt-test")
					require.NotNil(t, result)
					usage, err = result.usage, streamErr
				} else {
					result, streamErr := svc.handleStreamingResponsePassthrough(ctx, resp, c, account, time.Now(), "gpt-test", "gpt-test")
					require.NotNil(t, result)
					usage, err = result.usage, streamErr
				}
				require.Error(t, err)
				var sent *PluginTransportError
				require.ErrorAs(t, finishGroupSmartWSAttempt(attempt, err), &sent)
				require.True(t, sent.RequestSent)
				require.True(t, IsResponseCommitted(c), "handler fallback must preserve the delivered terminal")
				var failover *UpstreamFailoverError
				require.NotErrorAs(t, err, &failover)
				require.Equal(t, stream.inputTokens, usage.InputTokens)
				events, exists := c.Get(OpsUpstreamErrorsKey)
				require.True(t, exists)
				recorded := events.([]*OpsUpstreamErrorEvent)
				require.Len(t, recorded, 1)
				if stream.inputTokens > 0 {
					require.NotNil(t, recorded[0].FailedUsage)
					require.Equal(t, stream.inputTokens, recorded[0].FailedUsage.InputTokens)
					require.Contains(t, w.Body.String(), `"input_tokens":17`)
				} else {
					require.Nil(t, recorded[0].FailedUsage)
					require.Contains(t, w.Body.String(), "resp_test")
					require.NotContains(t, w.Body.String(), `"usage"`)
				}
				require.Equal(t, 1, strings.Count(w.Body.String(), `"type":"response.failed"`))
				require.NotContains(t, w.Body.String(), `"type":"error"`)
			})
		}
	}
}
