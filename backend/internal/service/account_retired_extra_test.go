//go:build unit

package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripRetiredAccountExtraPreservesActiveFeatures(t *testing.T) {
	retained := map[string]any{
		"codex_fingerprint_mode":      "single_machine_multi_window",
		"codex_fingerprint_seed":      "stable-device",
		"proxy_ids":                   []int64{7, 8},
		"enable_tls_fingerprint":      true,
		"upstream_billing_rate_limit": 1.5,
		"model_rate_limits":           map[string]any{"gpt-5.5": "cooldown"},
		"codex_usage_updated_at":      "2026-10-13T00:00:00Z",
	}
	legacy := StripRetiredAccountExtra(retained)
	for _, key := range []string{
		"codex_turn_ticket:gpt-5.5", "codex_turn_cookies", "codex_harvest_proxy_url",
		"codex_ticket_enabled", "codex_ticket_fail_closed", "codex_ticket_harvest_proxy_ids",
	} {
		legacy[key] = "obsolete"
	}
	clean := StripRetiredAccountExtra(legacy)
	require.Equal(t, retained, clean)
	require.Len(t, legacy, len(retained)+6)
	require.Equal(t, clean, StripRetiredAccountExtra(clean))
	require.Nil(t, StripRetiredAccountExtra(nil))
}
