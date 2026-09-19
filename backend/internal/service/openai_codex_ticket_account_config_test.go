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

func TestResolveOpenAICodexTicketAccountConfig_GatewayIsMasterSwitch(t *testing.T) {
	for _, global := range []bool{false, true} {
		for _, enabled := range []bool{false, true} {
			account := ticketTestAccount(1)
			account.Extra = map[string]any{OpenAICodexTicketEnabledExtraKey: enabled}
			policy := ResolveOpenAICodexTicketAccountConfig(account, config.OpenAICodexTicketConfig{Enabled: global})
			require.Equal(t, global, policy.GatewayEnabled)
			require.Equal(t, enabled, policy.AccountEnabled)
			require.Equal(t, global && enabled, policy.Enabled)
			require.True(t, policy.FailClosed)
		}
	}
	account := ticketTestAccount(1)
	policy := ResolveOpenAICodexTicketAccountConfig(account, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: false})
	require.True(t, policy.Enabled, "untouched legacy accounts preserve their gateway policy")
	require.False(t, policy.FailClosed)
	account.Type = AccountTypeAPIKey
	require.False(t, ResolveOpenAICodexTicketAccountConfig(account, config.OpenAICodexTicketConfig{Enabled: true}).Enabled)
}

func TestOpenAICodexTicketAccountPolicy_GatewayOffStopsHarvestInjectionAndBlocking(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: false, FailClosed: true, HarvestProxyURL: "http://pool.example:8080",
	}, upstream)
	account := ticketTestAccount(11)
	account.Status = StatusActive
	account.Extra = map[string]any{OpenAICodexTicketEnabledExtraKey: true}
	svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*account}}
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	svc.refreshOpenAICodexTickets(context.Background())
	svc.openaiCodexTicketScheduler.workers.Wait()
	require.Empty(t, upstream.requests)
	require.Empty(t, OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now()))
	require.True(t, svc.openAICodexTicketAccountConfig(context.Background(), account).AccountEnabled)

	// Re-enabling the gateway restores the account choice without editing it.
	svc.cfg.Gateway.OpenAICodexTicket.Enabled = true
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-5.5"))
	account.Extra[OpenAICodexTicketEnabledExtraKey] = false
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
}

func TestOpenAICodexTicketAccountPolicy_GatewayHotTogglePreservesCachedTicketAndAccountChoice(t *testing.T) {
	ctx := context.Background()
	key := SettingKeyOpenAICodexTicketEnabled
	repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{key: "true"}}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false}, nil)
	svc.settingService = NewSettingService(repo, svc.cfg)
	account := ticketTestAccount(11)
	account.Extra = map[string]any{OpenAICodexTicketEnabledExtraKey: true}
	state := fakeCodexTicketState(292)
	svc.storeOpenAICodexTicket(ctx, account, &openAICodexTicket{
		AccountID: account.ID, Model: "gpt-6-astra", State: state, Length: len(state),
		CapturedAt: time.Now(), ExpiresAt: time.Now().Add(time.Hour),
	})
	h := http.Header{}
	require.NoError(t, svc.applyOpenAICodexTicket(ctx, account, "gpt-6-astra", h))
	require.Equal(t, state, h.Get(openAICodexTurnStateHeader))

	// Runtime settings override both static configuration and an enabled account.
	svc.cfg.Gateway.OpenAICodexTicket.Enabled = true
	repo.values[key] = "false"
	svc.settingService.InvalidateOpenAICodexTicketEnabledCache()
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.NoError(t, svc.applyOpenAICodexTicket(ctx, account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader), "disabled gateway must not inject even a valid cached ticket")
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-5.6-sol"), "disabled gateway must not block a missing ticket")
	policy := svc.openAICodexTicketAccountConfig(ctx, account)
	require.False(t, policy.Enabled)
	require.True(t, policy.AccountEnabled)

	repo.values[key] = "true"
	svc.settingService.InvalidateOpenAICodexTicketEnabledCache()
	require.NoError(t, svc.applyOpenAICodexTicket(ctx, account, "gpt-6-astra", h))
	require.Equal(t, state, h.Get(openAICodexTurnStateHeader))
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-5.6-sol"))
}

func TestOpenAICodexTicketAccountPolicy_ValidTicketAllowsThenExpiryBlocks(t *testing.T) {
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled:      true,
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

func TestOpenAICodexTicketProbe_RotatesSharedGatewayPoolForEachAccount(t *testing.T) {
	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, fakeCodexTicketState(292))
	upstream := &codexTicketProxyRecordingUpstream{responses: []*http.Response{
		{StatusCode: http.StatusServiceUnavailable, Header: http.Header{}, Body: http.NoBody},
		{StatusCode: http.StatusOK, Header: h, Body: http.NoBody},
		{StatusCode: http.StatusOK, Header: h, Body: http.NoBody},
	}}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: true, FailClosed: true,
		HarvestProxyURL: "http://proxy-a.example:8080\nsocks5h://proxy-b.example:1080",
	}, upstream)
	account := ticketTestAccount(14)
	businessID := int64(123)
	account.ProxyID = &businessID
	account.Proxy = &Proxy{ID: businessID, Protocol: "http", Host: "business.example", Port: 8080}
	account.Extra = map[string]any{OpenAICodexTicketEnabledExtraKey: true, OpenAICodexTicketHarvestProxyIDsExtraKey: []int64{999}}
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
	require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	svc.probeOnceOpenAICodexTicket(context.Background(), ticketTestAccount(15), "gpt-6-astra")
	dueTicketJobs(svc)
	svc.probeOnceOpenAICodexTicket(context.Background(), account, "gpt-6-astra")
	require.Equal(t, []string{"http://proxy-a.example:8080", "socks5h://proxy-b.example:1080", "socks5h://proxy-b.example:1080"}, upstream.proxies)
	require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
	require.Equal(t, businessID, *account.ProxyID)
}

func TestOpenAICodexTicketTargetLength_FollowsPlan(t *testing.T) {
	for plan, expected := range map[string]int{
		"pro": 292, "chatgpt_pro": 292, "Pro Lite": 292,
		"team": 332, " TEAM ": 332, "chatgpt_team": 332,
		"business": 332, "self_serve_business_prolite": 332,
		"self_serve_business_usage_based": 332, "plus": 292, "": 292,
	} {
		t.Run(plan, func(t *testing.T) {
			account := ticketTestAccount(1)
			account.Credentials["plan_type"] = plan
			require.Equal(t, expected, openAICodexTicketTargetLength(account, config.OpenAICodexTicketConfig{}))
		})
	}
	account := ticketTestAccount(1)
	account.Credentials["chatgpt_plan_type"] = "team"
	require.Equal(t, 332, openAICodexTicketTargetLength(account, config.OpenAICodexTicketConfig{TargetLength: 292}))
	account.Credentials["plan_type"] = "pro"
	require.Equal(t, 292, openAICodexTicketTargetLength(account, config.OpenAICodexTicketConfig{TargetLength: 332}))
}

func TestOpenAICodexTicketPlans_HarvestValidateInjectAndReportSameLength(t *testing.T) {
	for _, plan := range []string{"pro", "team"} {
		t.Run(plan, func(t *testing.T) {
			account := ticketTestAccount(21)
			account.Status = StatusActive
			account.Credentials["plan_type"] = plan
			target, wrong := 292, 332
			if plan == "team" {
				target, wrong = wrong, target
			}
			response := func(length int) *http.Response {
				header := http.Header{}
				header.Set(openAICodexTurnStateHeader, fakeCodexTicketState(length))
				return &http.Response{StatusCode: 200, Header: header, Body: http.NoBody}
			}
			upstream := &httpUpstreamRecorder{responses: []*http.Response{response(wrong), response(target)}}
			svc := ticketTestService(t, config.OpenAICodexTicketConfig{
				Enabled: true, FailClosed: true, TargetLength: 292,
				HarvestProxyURL: "http://pool.example:8080", Models: []string{"gpt-6-astra"},
			}, upstream)
			repo := &codexTicketRefreshRepo{accounts: []Account{*account}}
			svc.accountRepo = repo
			svc.refreshOpenAICodexTickets(context.Background())
			svc.openaiCodexTicketScheduler.workers.Wait()
			require.Nil(t, svc.lookupOpenAICodexTicket(account, "gpt-6-astra"))
			require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
			dueTicketJobs(svc)
			svc.refreshOpenAICodexTickets(context.Background())
			svc.openaiCodexTicketScheduler.workers.Wait()
			ticket := svc.lookupOpenAICodexTicket(account, "gpt-6-astra")
			require.NotNil(t, ticket)
			require.Equal(t, target, ticket.Length)
			require.Equal(t, account.GetChatGPTAccountID(), upstream.requests[1].Header.Get("chatgpt-account-id"))
			header := http.Header{}
			require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", header))
			require.Len(t, header.Get(openAICodexTurnStateHeader), target)
			require.False(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
			// A valid Team ticket must suppress new probes just like a Pro ticket.
			svc.refreshOpenAICodexTickets(context.Background())
			svc.openaiCodexTicketScheduler.workers.Wait()
			require.Len(t, upstream.requests, 2)
			account.Extra = repo.updates
			status := OpenAICodexTicketStatuses(account, svc.openAICodexTicketConfig(), time.Now())
			require.Len(t, status, 1)
			require.True(t, status[0].Ready)
			require.Equal(t, target, status[0].TargetLength)
			require.Equal(t, target, status[0].Length)
			// Changing a plan cannot reuse a ticket of the former plan's length.
			if plan == "team" {
				account.Credentials["plan_type"] = "pro"
			} else {
				account.Credentials["plan_type"] = "team"
			}
			require.True(t, svc.openAICodexTicketBlocksAccount(account, "gpt-6-astra"))
			require.ErrorIs(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", http.Header{}), ErrOpenAICodexTicketUnavailable)
		})
	}
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
