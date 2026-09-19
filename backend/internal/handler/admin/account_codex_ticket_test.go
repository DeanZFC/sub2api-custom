package admin

import (
	"encoding/json"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/handler/dto"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"strings"
	"testing"
)

func TestAccountResponseCodexTicketAccountPolicyOverridesGateway(t *testing.T) {
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Extra: map[string]any{
		service.OpenAICodexTicketEnabledExtraKey:         true,
		service.OpenAICodexTicketFailClosedExtraKey:      false,
		service.OpenAICodexTicketHarvestProxyIDsExtraKey: []any{float64(8), float64(2)},
		"codex_turn_ticket:gpt-6-astra":                  map[string]any{"state": "private-ticket-material"},
	}}
	h := &AccountHandler{cfg: &config.Config{}}
	for _, got := range []*dto.Account{h.accountResponseFromService(account), h.accountListResponseFromService(account)} {
		require.NotNil(t, got.CodexTicketConfig)
		require.True(t, got.CodexTicketConfig.Enabled)
		require.False(t, got.CodexTicketConfig.FailClosed)
		require.Equal(t, []int64{8, 2}, got.CodexTicketConfig.HarvestProxyIDs)
		require.False(t, got.CodexTicketConfig.LegacyProxyFallback)
		require.NotEmpty(t, got.CodexTurnTickets)
		payload, err := json.Marshal(got)
		require.NoError(t, err)
		require.False(t, strings.Contains(string(payload), "private-ticket-material"))
		require.NotContains(t, got.Extra, "codex_turn_ticket:gpt-6-astra")
		compact := dto.AccountListItemFromAccount(got)
		require.Equal(t, got.CodexTicketConfig, compact.CodexTicketConfig)
	}
}

func TestAccountResponseCodexTicketsUsesConfiguredPolicy(t *testing.T) {
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth}
	h := &AccountHandler{cfg: &config.Config{}}
	require.Empty(t, h.accountResponseFromService(account).CodexTurnTickets)
	require.Empty(t, h.accountListResponseFromService(account).CodexTurnTickets)
	h.cfg.Gateway.OpenAICodexTicket = config.OpenAICodexTicketConfig{Enabled: true, Models: []string{"configured-model"}, FailClosed: false}
	status := h.accountListResponseFromService(account).CodexTurnTickets
	require.Len(t, status, 1)
	require.Equal(t, "configured-model", status[0].Model)
	require.False(t, status[0].Blocked)
	h.cfg.Gateway.OpenAICodexTicket.FailClosed = true
	require.True(t, h.accountResponseFromService(account).CodexTurnTickets[0].Blocked)
}

func TestAccountResponseCodexTicketsReadsLiveSettingsAfterRestart(t *testing.T) {
	cfg := &config.Config{}
	repo := &settingHandlerRepoStub{values: map[string]string{service.SettingKeyOpenAICodexTicketEnabled: "true"}}
	settings := service.NewSettingService(repo, cfg)
	h := &AccountHandler{cfg: cfg}
	h.SetCodexTicketSettings(settings)
	account := &service.Account{ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeSetupToken}
	require.Len(t, h.accountListResponseFromService(account).CodexTurnTickets, 2)
	require.False(t, cfg.Gateway.OpenAICodexTicket.Enabled)
	repo.values[service.SettingKeyOpenAICodexTicketEnabled] = "false"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.Empty(t, h.accountResponseFromService(account).CodexTurnTickets)
}
