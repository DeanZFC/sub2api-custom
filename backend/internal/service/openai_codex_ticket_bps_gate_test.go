package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestCodexTicketsRespectGlobalBPSGate(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "disabled", true: "enabled"}[enabled], func(t *testing.T) {
			settings := &excelBPSImageSettingsRepo{values: map[string]string{SettingKeyExcelBPSEnabled: map[bool]string{false: "false", true: "true"}[enabled]}}
			gateway := ticketTestService(t, config.OpenAICodexTicketConfig{Enabled: true, FailClosed: true, Models: []string{"gpt-6-astra"}}, nil)
			gateway.settingService = NewSettingService(settings, gateway.cfg)
			account := ticketTestAccount(1)
			account.Extra = map[string]any{"openai_excel_bps": true, "openai_excel_bps_models": []any{"gpt-6-astra"}}
			require.Equal(t, !enabled, gateway.isOpenAICodexTicketAccount(context.Background(), account, "gpt-6-astra"))
			require.True(t, gateway.isOpenAICodexTicketAccount(context.Background(), account, "gpt-6-sol"))
			require.Equal(t, true, account.Extra["openai_excel_bps"], "runtime checks must not mutate configured routing")
			account.Credentials["client_id"] = openai.ExcelClientID
			require.False(t, gateway.isOpenAICodexTicketAccount(context.Background(), account, "gpt-6-astra"), "primary Excel grants cannot harvest native tickets")
		})
	}
}
