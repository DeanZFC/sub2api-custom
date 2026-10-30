//go:build unit

package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestStripRetiredAccountExtraPreservesActiveFeatures(t *testing.T) {
	retained := map[string]any{
		"codex_fingerprint_mode": "full",
		"codex_fingerprint_seed": "stable-device",
		"proxy_ids":              []int64{7, 8},
		"enable_tls_fingerprint": true,
		"model_rate_limits":      map[string]any{"gpt-5.5": "cooldown"},
		"codex_usage_updated_at": "2026-10-13T00:00:00Z",
	}
	legacy := StripRetiredAccountExtra(retained)
	for _, key := range []string{
		"upstream_billing_rate_limit", "codex_turn_ticket:gpt-5.5", "codex_turn_cookies", "codex_harvest_proxy_url",
		"codex_ticket_enabled", "codex_ticket_fail_closed", "codex_ticket_harvest_proxy_ids",
	} {
		legacy[key] = "obsolete"
	}
	clean := StripRetiredAccountExtra(legacy)
	require.Equal(t, retained, clean)
	require.Len(t, legacy, len(retained)+7)
	require.Equal(t, clean, StripRetiredAccountExtra(clean))
	require.Nil(t, StripRetiredAccountExtra(nil))
}

func TestStripRetiredAccountExtraNormalizesLegacyFingerprintWithoutMutatingInput(t *testing.T) {
	legacy := map[string]any{"codex_fingerprint_mode": " single_machine_multi_window ", "codex_fingerprint_seed": "stable", "upstream_billing_rate_limit": 0.0, "upstream_billing_probe_enabled": false}
	clean := StripRetiredAccountExtra(legacy)
	require.Equal(t, "off", clean["codex_fingerprint_mode"])
	require.Equal(t, "stable", clean["codex_fingerprint_seed"])
	require.Equal(t, false, clean["upstream_billing_probe_enabled"])
	require.NotContains(t, clean, "upstream_billing_rate_limit")
	require.Equal(t, " single_machine_multi_window ", legacy["codex_fingerprint_mode"])
	require.Contains(t, legacy, "upstream_billing_rate_limit")
	for _, mode := range []string{"off", "device", "session", "full"} {
		extra := map[string]any{"codex_fingerprint_mode": mode}
		require.Equal(t, extra, StripRetiredAccountExtra(extra))
	}
}

func TestRetiredRateCeilingDoesNotAffectSchedulingOrProbePreference(t *testing.T) {
	disabled := false
	extra := map[string]any{"upstream_billing_rate_limit": 0.0, UpstreamBillingProbeEnabledExtraKey: false}
	created, err := buildAccountForCreate(&CreateAccountInput{Platform: PlatformOpenAI, Type: AccountTypeAPIKey, ProbeEnabled: &disabled}, extra)
	require.NoError(t, err)
	require.NotContains(t, created.Extra, "upstream_billing_rate_limit")
	require.NotEqual(t, true, created.Extra[UpstreamBillingProbeEnabledExtraKey])

	// A scheduler snapshot predating migration must also ignore the retired limit.
	account := &Account{ID: 1, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example"},
		Extra: map[string]any{"upstream_billing_rate_limit": 0.0, UpstreamBillingProbeEnabledExtraKey: true,
			UpstreamBillingProbeExtraKey: &UpstreamBillingProbeSnapshot{Status: UpstreamBillingProbeStatusOK,
				Data: map[string]any{"billing_scope": "token", "resolved_rate_multiplier": 10.0}}}}
	require.True(t, account.IsSchedulable())
	account.Schedulable = false
	require.False(t, account.IsSchedulable(), "manual scheduling pause must remain authoritative")
	account.Schedulable = true
	repo := &upstreamBillingProbeAdminRepo{&upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{1: account}}}
	svc := &adminServiceImpl{accountRepo: repo}
	updated, err := svc.UpdateAccount(context.Background(), 1, &UpdateAccountInput{ProbeEnabled: &disabled})
	require.NoError(t, err)
	require.Equal(t, false, updated.Extra[UpstreamBillingProbeEnabledExtraKey])
	require.NotContains(t, updated.Extra, "upstream_billing_rate_limit")
}
