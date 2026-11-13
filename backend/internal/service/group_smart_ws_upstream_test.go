package service

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
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
