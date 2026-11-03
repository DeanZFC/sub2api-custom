package service

import (
	"context"
	"errors"
	"maps"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type ticketSendAccountRepo struct {
	AccountRepository
	account *Account
	err     error
	reads   int
}

func (r *ticketSendAccountRepo) GetByID(context.Context, int64) (*Account, error) {
	r.reads++
	return r.account, r.err
}

func TestCodexTicketSendRechecksPersistedAccount(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Account)
	}{
		{"disabled", func(a *Account) { a.Schedulable = false }},
		{"status", func(a *Account) { a.Status = "error" }},
		{"model_rate_limit", func(a *Account) {
			a.Extra[modelRateLimitsKey] = map[string]any{"gpt-6-astra": map[string]any{"rate_limit_reset_at": time.Now().Add(time.Hour).Format(time.RFC3339)}}
		}},
		{"type", func(a *Account) { a.Type = AccountTypeAPIKey }},
		{"identity", func(a *Account) { a.Credentials["chatgpt_account_id"] = "replaced" }},
		{"token", func(a *Account) { a.Credentials["access_token"] = "refreshed" }},
		{"groups", func(a *Account) { a.GroupIDs = []int64{2} }},
		{"proxy", func(a *Account) { id := int64(9); a.ProxyID = &id }},
		{"bps_protocol", func(a *Account) { a.Extra["openai_excel_bps"] = true }},
		{"vm_route", func(a *Account) { a.Extra["openai_vm_bound"] = true }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			selected := ticketTestAccount(801)
			fresh := *selected
			fresh.Credentials = maps.Clone(selected.Credentials)
			fresh.Extra = map[string]any{}
			tc.change(&fresh)
			repo := &ticketSendAccountRepo{account: &fresh}
			svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
			svc.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}
			out, err := svc.refreshCodexTicketAccountForSend(context.Background(), selected, "gpt-6-astra")
			require.Nil(t, out)
			require.True(t, IsOpenAITurnAdmissionError(err), "%v", err)
			require.Equal(t, 1, repo.reads)
		})
	}
}

func TestCodexTicketSendReadsFreshTicketsAndPreservesSelectedProxy(t *testing.T) {
	account := ticketTestAccount(802)
	account.ProxyIDs = []int64{7, 8}
	account.Proxies = []*Proxy{{ID: 7, Status: StatusActive, Protocol: "http", Host: "proxy.invalid", Port: 8080}}
	fresh := *account
	fresh.Extra = map[string]any{}
	attachReadyCodexTicket(&fresh, "gpt-6-astra")
	selected, err := account.WithProxyRoute(7)
	require.NoError(t, err)
	repo := &ticketSendAccountRepo{account: &fresh}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
	svc.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}
	out, err := svc.refreshCodexTicketAccountForSend(context.Background(), selected, "gpt-6-astra")
	require.NoError(t, err)
	require.Equal(t, int64(7), out.SelectedProxyID)
	require.Contains(t, out.Extra, openAICodexTicketExtraKey("gpt-6-astra"))
	require.Empty(t, account.Extra, "shared selection snapshot must not change")
	repo.err = errors.New("database unavailable")
	_, err = svc.refreshCodexTicketAccountForSend(context.Background(), selected, "gpt-6-astra")
	require.True(t, IsOpenAITurnAdmissionError(err))
	repo.reads = 0
	_, err = svc.refreshCodexTicketAccountForSend(context.Background(), selected, "ordinary-model")
	require.NoError(t, err)
	require.Zero(t, repo.reads, "ungated traffic retains the existing fast path")
}

func TestCodexTicketSendAcceptsCurrentTokenAfterProviderRefresh(t *testing.T) {
	selected := ticketTestAccount(803)
	fresh := *selected
	fresh.Credentials = maps.Clone(selected.Credentials)
	fresh.Credentials["access_token"] = "current-access"
	fresh.Credentials["refresh_token"] = "rotated-refresh"
	fresh.Credentials["_token_version"] = 2
	fresh.Credentials["expires_at"] = "2027-01-01T00:00:00Z"
	repo := &ticketSendAccountRepo{account: &fresh}
	svc := &OpenAIGatewayService{accountRepo: repo, cfg: &config.Config{}}
	svc.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"gpt-6-astra"}}
	for _, auth := range []string{"", "Bearer tok", "Bearer other"} {
		_, err := svc.refreshCodexTicketAccountForSend(context.Background(), selected, "gpt-6-astra", auth)
		require.True(t, IsOpenAITurnAdmissionError(err), "stale request must not adopt replaced credentials")
	}
	out, err := svc.refreshCodexTicketAccountForSend(context.Background(), selected, "gpt-6-astra", "Bearer current-access")
	require.NoError(t, err)
	require.Same(t, &fresh, out)
	fresh.Credentials["base_url"] = "https://changed.invalid"
	_, err = svc.refreshCodexTicketAccountForSend(context.Background(), selected, "gpt-6-astra", "Bearer current-access")
	require.True(t, IsOpenAITurnAdmissionError(err), "a fresh token must not authorize changed routing")
}

func TestTicketAdmissionSkipsOtherOpenAICredentialProtocols(t *testing.T) {
	for _, account := range []*Account{
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{"auth_mode": OpenAIAuthModePersonalAccessToken}},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth, Credentials: map[string]any{openAIAuthModeCredentialKey: OpenAIAuthModeAgentIdentity}},
	} {
		require.False(t, isOpenAICodexTicketAccount(account, "gpt-6-astra"))
	}
}
