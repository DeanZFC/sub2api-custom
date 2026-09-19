package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func TestResolveOpenAICodexTicketAccountConfig_AccountOverridesGatewayPolicy(t *testing.T) {
	tests := []struct {
		name            string
		account         *Account
		fallback        config.OpenAICodexTicketConfig
		wantEnabled     bool
		wantFailClosed  bool
		wantLegacyProxy bool
		wantHarvestIDs  []int64
	}{
		{
			name:            "legacy account inherits gateway policy",
			account:         ticketTestAccount(1),
			fallback:        config.OpenAICodexTicketConfig{Enabled: true, FailClosed: false},
			wantEnabled:     true,
			wantFailClosed:  false,
			wantLegacyProxy: true,
		},
		{
			name: "explicit enabled overrides disabled gateway and defaults fail closed",
			account: &Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				OpenAICodexTicketEnabledExtraKey: true,
			}},
			fallback:        config.OpenAICodexTicketConfig{Enabled: false, FailClosed: false},
			wantEnabled:     true,
			wantFailClosed:  true,
			wantLegacyProxy: true,
		},
		{
			name: "explicit disabled overrides enabled gateway",
			account: &Account{ID: 3, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				OpenAICodexTicketEnabledExtraKey: false,
			}},
			fallback:        config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true},
			wantEnabled:     false,
			wantFailClosed:  true,
			wantLegacyProxy: true,
		},
		{
			name: "explicit fail open is respected",
			account: &Account{ID: 4, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				OpenAICodexTicketEnabledExtraKey:    true,
				OpenAICodexTicketFailClosedExtraKey: false,
			}},
			fallback:        config.OpenAICodexTicketConfig{Enabled: false, FailClosed: true},
			wantEnabled:     true,
			wantFailClosed:  false,
			wantLegacyProxy: true,
		},
		{
			name: "explicit empty proxy list disables legacy fallback",
			account: &Account{ID: 5, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
				OpenAICodexTicketEnabledExtraKey:         true,
				OpenAICodexTicketHarvestProxyIDsExtraKey: []any{},
			}},
			fallback:        config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true},
			wantEnabled:     true,
			wantFailClosed:  true,
			wantLegacyProxy: false,
			wantHarvestIDs:  []int64{},
		},
		{
			name: "non oauth account never owns tickets",
			account: &Account{ID: 6, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Extra: map[string]any{
				OpenAICodexTicketEnabledExtraKey: true,
			}},
			fallback:        config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true},
			wantEnabled:     false,
			wantFailClosed:  true,
			wantLegacyProxy: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveOpenAICodexTicketAccountConfig(tt.account, tt.fallback)
			require.Equal(t, tt.wantEnabled, got.Enabled)
			require.Equal(t, tt.wantFailClosed, got.FailClosed)
			require.Equal(t, tt.wantLegacyProxy, got.LegacyProxyFallback)
			require.Equal(t, append([]int64{}, tt.wantHarvestIDs...), got.HarvestProxyIDs)
		})
	}
}

func TestOpenAICodexTicketAccountPolicy_AppliesWhenGatewaySwitchIsOff(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      false,
		TargetLength: 292,
		FailClosed:   false,
		Models:       []string{"gpt-6-astra"},
	}, nil)
	account := &Account{ID: 11, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		OpenAICodexTicketEnabledExtraKey: true,
	}}

	// The account policy owns the feature after it is explicitly configured. A
	// missing ticket therefore blocks the gated model even though the old global
	// switch is disabled; the default for a newly opted-in account is fail-closed.
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	h := http.Header{}
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h), ErrOpenAICodexTicketUnavailable)

	// Models outside the account's ticket model policy continue normally.
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-5.5"))
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-5.5", h))

	// Explicitly opting out leaves the client supplied header untouched and
	// removes the account from ticket gating.
	account.Extra[OpenAICodexTicketEnabledExtraKey] = false
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
}

func TestOpenAICodexTicketAccountPolicy_ValidTicketAllowsThenExpiryBlocks(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      false,
		TargetLength: 292,
		Models:       []string{"gpt-6-astra"},
	}, nil)
	account := &Account{ID: 12, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		OpenAICodexTicketEnabledExtraKey: true,
	}}
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	state := fakeCodexTicketState(292)
	ticket := &openAICodexTicket{
		AccountID:  account.ID,
		Model:      "gpt-6-astra",
		State:      state,
		Length:     292,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	}
	svc.storeOpenAICodexTicket(context.Background(), account, ticket)
	h := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, state, h.Get(openAICodexTurnStateHeader))
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))

	// A previously usable account becomes unavailable for this model when its
	// ticket expires; a stale cached ticket must not keep the account schedulable.
	expired := *ticket
	expired.ExpiresAt = time.Now().Add(-time.Second)
	svc.storeOpenAICodexTicket(context.Background(), account, &expired)
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	h = http.Header{}
	require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h), ErrOpenAICodexTicketUnavailable)
	require.Empty(t, h.Get(openAICodexTurnStateHeader))
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-5.5"))
}

func TestOpenAICodexTicketAccountPolicy_FailOpenOverridesGatewayFailClosed(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      true,
		FailClosed:   true,
		TargetLength: 292,
		Models:       []string{"gpt-6-astra"},
	}, nil)
	account := &Account{ID: 13, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Extra: map[string]any{
		OpenAICodexTicketEnabledExtraKey:    true,
		OpenAICodexTicketFailClosedExtraKey: false,
	}}
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
}

type codexTicketProxyRepoForAccountPolicy struct {
	ProxyRepository
	proxies []Proxy
}

func (r *codexTicketProxyRepoForAccountPolicy) ListByIDs(_ context.Context, _ []int64) ([]Proxy, error) {
	return r.proxies, nil
}

func TestOpenAICodexTicketHarvestProxies_FiltersAndPreservesConfiguredOrder(t *testing.T) {
	expiredAt := time.Now().Add(-time.Minute)
	repo := &codexTicketProxyRepoForAccountPolicy{proxies: []Proxy{
		{ID: 2, Protocol: "http", Host: "second.example", Port: 8080, Status: StatusActive},
		{ID: 1, Protocol: "socks5h", Host: "first.example", Port: 1080, Status: StatusActive},
		{ID: 3, Protocol: "http", Host: "inactive.example", Port: 8080, Status: "inactive"},
		{ID: 4, Protocol: "http", Host: "expired.example", Port: 8080, Status: StatusActive, ExpiresAt: &expiredAt},
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:         false,
		HarvestProxyURL: "http://legacy.example:8080",
	}, nil)
	svc.codexTicketProxyRepo = repo
	policy := OpenAICodexTicketAccountConfig{
		HarvestProxyIDs:     []int64{1, 3, 4, 2},
		LegacyProxyFallback: false,
	}
	got := svc.openAICodexTicketHarvestProxies(context.Background(), policy)
	require.Equal(t, []openAICodexTicketHarvestProxy{
		{id: 1, url: "socks5h://first.example:1080"},
		{id: 2, url: "http://second.example:8080"},
	}, got)
}

func TestOpenAICodexTicketHarvestProxies_ExplicitIDsDoNotFallBackToGatewayProxy(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:         true,
		HarvestProxyURL: "http://legacy.example:8080",
	}, nil)
	svc.codexTicketProxyRepo = &codexTicketProxyRepoForAccountPolicy{}
	got := svc.openAICodexTicketHarvestProxies(context.Background(), OpenAICodexTicketAccountConfig{
		HarvestProxyIDs:     []int64{99},
		LegacyProxyFallback: false,
	})
	require.Empty(t, got)
}

func TestOpenAICodexTicketProbe_RotatesAccountHarvestProxiesAfterMiss(t *testing.T) {
	state := fakeCodexTicketState(292)
	response := func() *http.Response {
		h := http.Header{}
		h.Set(openAICodexTurnStateHeader, state)
		return &http.Response{StatusCode: http.StatusOK, Header: h, Body: http.NoBody}
	}
	upstream := &codexTicketProxyRecordingUpstream{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody},
		response(),
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:                      false,
		TargetLength:                 292,
		TTLSeconds:                   3600,
		HarvestAttemptTimeoutSeconds: 5,
		HarvestProxyURL:              "http://legacy.example:8080",
		Models:                       []string{"gpt-6-astra"},
	}, upstream)
	svc.codexTicketProxyRepo = &codexTicketProxyRepoForAccountPolicy{proxies: []Proxy{
		{ID: 10, Protocol: "http", Host: "proxy-a.example", Port: 8080, Status: StatusActive},
		{ID: 11, Protocol: "http", Host: "proxy-b.example", Port: 8080, Status: StatusActive},
	}}
	account := &Account{ID: 14, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{
		"access_token": "token",
	}, Extra: map[string]any{
		OpenAICodexTicketEnabledExtraKey:         true,
		OpenAICodexTicketHarvestProxyIDsExtraKey: []int64{10, 11},
	}}

	// Direct probe calls model two consecutive refresh attempts. The per-account
	// cursor rotates through the configured proxy pool and never uses the legacy
	// gateway proxy.
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Equal(t, []string{"http://proxy-a.example:8080"}, upstream.proxies)
	require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Equal(t, []string{
		"http://proxy-a.example:8080",
		"http://proxy-b.example:8080",
	}, upstream.proxies)
	require.NotNil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}

type codexTicketProxyRecordingUpstream struct {
	proxies   []string
	responses []*http.Response
}

func (u *codexTicketProxyRecordingUpstream) Do(_ *http.Request, proxyURL string, _ int64, _ int) (*http.Response, error) {
	u.proxies = append(u.proxies, proxyURL)
	response := u.responses[0]
	u.responses = u.responses[1:]
	return response, nil
}

func (u *codexTicketProxyRecordingUpstream) DoWithTLS(req *http.Request, proxyURL string, accountID int64, accountConcurrency int, _ *tlsfingerprint.Profile) (*http.Response, error) {
	return u.Do(req, proxyURL, accountID, accountConcurrency)
}
