package service

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type codexTicketSettingRepo struct {
	*codexPolicyMigrationRepoStub
	err error
}

func (r *codexTicketSettingRepo) GetValue(ctx context.Context, key string) (string, error) {
	if r.err != nil {
		return "", r.err
	}
	return r.codexPolicyMigrationRepoStub.GetValue(ctx, key)
}

func TestCodexTicketEnabledRuntimeSettingOverridesYaml(t *testing.T) {
	repo := &codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{}}}
	settings := NewSettingService(repo, &config.Config{})
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: false, FailClosed: true}, nil)
	svc.settingService = settings
	account := ticketTestAccount(41)
	svc.storeOpenAICodexTicket(context.Background(), account, &openAICodexTicket{
		AccountID:  41,
		Model:      "gpt-6-astra",
		State:      fakeCodexTicketState(292),
		Length:     292,
		CapturedAt: time.Now(),
		ExpiresAt:  time.Now().Add(time.Hour),
	})

	h := http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.False(t, svc.openAICodexTicketEnabled())
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))

	repo.values[SettingKeyOpenAICodexTicketEnabled] = "true"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.True(t, svc.openAICodexTicketEnabled())
	h = http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, fakeCodexTicketState(292), h.Get(openAICodexTurnStateHeader))

	repo.values[SettingKeyOpenAICodexTicketEnabled] = "false"
	settings.InvalidateOpenAICodexTicketEnabledCache()
	require.False(t, svc.openAICodexTicketEnabled())
	h = http.Header{}
	h.Set(openAICodexTurnStateHeader, "client-state")
	require.NoError(t, svc.applyOpenAICodexTicket(context.Background(), account, "gpt-6-astra", h))
	require.Equal(t, "client-state", h.Get(openAICodexTurnStateHeader))
}

func TestRefreshOpenAICodexTickets_DisabledSkipsHarvest(t *testing.T) {
	upstream := &httpUpstreamRecorder{}
	svc := ticketTestService(t, config.OpenAICodexTicketConfig{
		Enabled: false,
	}, upstream)
	svc.accountRepo = &codexTicketRefreshRepo{accounts: []Account{*ticketTestAccount(41)}}
	svc.refreshOpenAICodexTickets(context.Background())
	require.Empty(t, upstream.requests)
}

func TestCodexTicketAccountProxyValidationDoesNotLeakCredentials(t *testing.T) {
	for _, raw := range []string{"http://user:secret@proxy.example.com:8080", "socks5h://user:secret@proxy.example.com:1080", "https://user:secret@[::1]:443"} {
		require.NoError(t, ValidateOpenAICodexTicketHarvestProxyURL(raw))
	}
	for _, raw := range []string{"user:secret@host:1234", "http://user:secret@", "ftp://user:secret@host:1234", "http://user:secret@host:99999", "http://host:1234/?password=secret", "http://host:1234/#secret", "http://user:secret%zz@host:1234"} {
		err := ValidateOpenAICodexTicketHarvestProxyURL(raw)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "secret")
	}
}

func TestCodexTicketSettingsRefreshDoesNotMutateSharedConfig(t *testing.T) {
	cfg := &config.Config{}
	svc := NewSettingService(&codexTicketSettingRepo{codexPolicyMigrationRepoStub: &codexPolicyMigrationRepoStub{values: map[string]string{SettingKeyOpenAICodexTicketEnabled: "true"}}}, cfg)
	svc.refreshCachedSettings(&SystemSettings{OpenAICodexTicketEnabled: true})
	require.False(t, cfg.Gateway.OpenAICodexTicket.Enabled, "runtime settings must not write the shared immutable startup configuration")
	require.True(t, svc.GetOpenAICodexTicketEnabled(context.Background(), false))
}
