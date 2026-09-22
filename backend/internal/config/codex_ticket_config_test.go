package config

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeOpenAICodexTicketTiming(t *testing.T) {
	for _, tc := range []struct {
		name        string
		ttl         int
		refresh     int
		wantTTL     int
		wantRefresh int
	}{
		{name: "defaults", wantTTL: 240, wantRefresh: 60},
		{name: "legacy_defaults", ttl: 3600, refresh: 600, wantTTL: 240, wantRefresh: 60},
		{name: "legacy_custom_refresh", ttl: 3600, refresh: 300, wantTTL: 240, wantRefresh: 60},
		{name: "negative_values", ttl: -1, refresh: -1, wantTTL: 240, wantRefresh: 60},
		{name: "shorter_lifetime", ttl: 120, refresh: 20, wantTTL: 120, wantRefresh: 20},
		{name: "shorter_lifetime_default_refresh", ttl: 120, wantTTL: 120, wantRefresh: 30},
		{name: "refresh_exceeds_lifetime", ttl: 120, refresh: 180, wantTTL: 120, wantRefresh: 30},
		{name: "minimum_lifetime", ttl: 1, refresh: 600, wantTTL: 1, wantRefresh: 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := OpenAICodexTicketConfig{Enabled: true, TargetLength: 332, FailClosed: true,
				TTLSeconds: tc.ttl, RefreshBeforeSeconds: tc.refresh, Models: []string{"gpt-6-astra"}}
			got := NormalizeOpenAICodexTicketTiming(input)
			require.Equal(t, tc.wantTTL, got.TTLSeconds)
			require.Equal(t, tc.wantRefresh, got.RefreshBeforeSeconds)
			require.Greater(t, got.TTLSeconds, got.RefreshBeforeSeconds)
			require.Equal(t, got, NormalizeOpenAICodexTicketTiming(got), "normalization must be idempotent")
			require.Equal(t, tc.ttl, input.TTLSeconds, "normalization must not mutate caller configuration")
			got.TTLSeconds, got.RefreshBeforeSeconds = input.TTLSeconds, input.RefreshBeforeSeconds
			require.Equal(t, input, got, "unrelated ticket policy must be preserved")
		})
	}
}

func TestLoadOpenAICodexTicketTiming(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configYAML string
		envTTL     string
		envRefresh string
	}{
		{name: "defaults"},
		{name: "legacy_yaml", configYAML: "gateway:\n  openai_codex_ticket:\n    ttl_seconds: 3600\n    refresh_before_seconds: 600\n"},
		{name: "legacy_environment", envTTL: "3600", envRefresh: "300"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			resetViperWithJWTSecret(t)
			t.Setenv("GATEWAY_OPENAI_CODEX_TICKET_TTL_SECONDS", tc.envTTL)
			t.Setenv("GATEWAY_OPENAI_CODEX_TICKET_REFRESH_BEFORE_SECONDS", tc.envRefresh)
			if tc.configYAML != "" {
				configFile := filepath.Join(t.TempDir(), "config.yaml")
				require.NoError(t, os.WriteFile(configFile, []byte(tc.configYAML), 0o600))
				t.Setenv("CONFIG_FILE", configFile)
			}
			cfg, err := Load()
			require.NoError(t, err)
			require.Equal(t, 240, cfg.Gateway.OpenAICodexTicket.TTLSeconds)
			require.Equal(t, 60, cfg.Gateway.OpenAICodexTicket.RefreshBeforeSeconds)
		})
	}
}
