package service

import (
	"context"
	"maps"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/tlsfingerprint"
	"github.com/stretchr/testify/require"
)

func mode1V2CompatibilityAccount() *Account {
	return &Account{ID: 8701, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 4, Extra: map[string]any{
		AntiDegradationExtraKey: true,
		AntiDegradeMarkerExtraKey: map[string]any{
			"enabled": true, "mode": "mode1", "policy_version": 2, "max_concurrency": 4, "applied_concurrency": 4,
			"prev": map[string]any{"concurrency": 0, "codex_fingerprint_mode": "off", "enable_tls_fingerprint": nil, "tls_fingerprint_builtin": nil, "tls_fingerprint_profile_id": nil, "proxy_mode": nil},
		},
		codexFingerprintSeedExtraKey: newCodexFingerprintSeed(),
		codexFingerprintModeExtraKey: "device",
		"enable_tls_fingerprint":     true,
		"tls_fingerprint_builtin":    "nodejs24",
	}}
}

func TestAntiDegradeMode1V2KeepsConfiguredTransport(t *testing.T) {
	for _, enabled := range []bool{false, true} {
		t.Run(map[bool]string{false: "standard", true: "nodejs24"}[enabled], func(t *testing.T) {
			a := mode1V2CompatibilityAccount()
			a.Extra["enable_tls_fingerprint"] = enabled
			require.NoError(t, ValidateAccountProtectionConfiguration(a))
			require.Equal(t, enabled, a.IsTLSFingerprintEnabled())
			profile, err := resolveAccountTLSProfileForOpenAI(a, nil)
			require.NoError(t, err)
			if enabled {
				require.Equal(t, tlsfingerprint.BuiltinProfile("nodejs24"), profile)
			} else {
				require.Nil(t, profile)
			}
			profile, err = resolveProtectionTransport(a, &config.Config{})
			require.NoError(t, err)
			require.Nil(t, profile, "deployment kill switch still selects standard transport")
			delete(a.Extra, codexFingerprintSeedExtraKey)
			_, err = resolveProtectionTransport(a, nil)
			require.ErrorContains(t, err, "身份种子")
		})
	}
}

func TestAntiDegradeMode1V2ExplicitUpgradePreservesSnapshot(t *testing.T) {
	for _, editedConcurrency := range []bool{false, true} {
		t.Run(map[bool]string{false: "original ceiling", true: "edited ceiling"}[editedConcurrency], func(t *testing.T) {
			a := mode1V2CompatibilityAccount()
			if editedConcurrency {
				a.Concurrency = 5
				BoundAccountProtectionConcurrency(a)
			}
			originalSeed := a.Extra[codexFingerprintSeedExtraKey]
			originalSnapshot := maps.Clone(antiDegradePrevious(a))
			originalCeiling := a.Concurrency
			svc, _ := newAntiDegradeTestService(a)
			preview, err := svc.PreviewMode(context.Background(), a.ID, AntiDegradeMode1)
			require.NoError(t, err)
			require.True(t, preview.Eligible)
			require.Equal(t, 2, preview.PolicyVersion)
			require.Equal(t, "nodejs24", preview.TLSProfile)
			updated, err := svc.ApplyAntiDegradeMode(context.Background(), a.ID, AntiDegradeMode1)
			require.NoError(t, err)
			require.Equal(t, mode1PolicyVersion, mode1Marker(updated)["policy_version"])
			require.Equal(t, originalSnapshot, antiDegradePrevious(updated))
			require.Equal(t, originalCeiling, updated.Concurrency)
			require.Equal(t, originalSeed, updated.Extra[codexFingerprintSeedExtraKey])
			profile, err := resolveProtectionTransport(updated, nil)
			require.NoError(t, err)
			require.Nil(t, profile)
			require.NotContains(t, updated.Extra, "tls_fingerprint_builtin")
			restored, err := svc.Revert(context.Background(), a.ID)
			require.NoError(t, err)
			if editedConcurrency {
				require.Equal(t, 5, restored.Concurrency)
			} else {
				require.Equal(t, 0, restored.Concurrency)
			}
			require.Equal(t, "off", restored.Extra[codexFingerprintModeExtraKey])
		})
	}
}

func TestAntiDegradeMode1V2UpgradeRequiresOriginalSnapshot(t *testing.T) {
	a := mode1V2CompatibilityAccount()
	delete(mode1Marker(a), "prev")
	svc, _ := newAntiDegradeTestService(a)
	preview, err := svc.PreviewMode(context.Background(), a.ID, AntiDegradeMode1)
	require.NoError(t, err)
	require.False(t, preview.Eligible)
	require.NotEmpty(t, preview.Issues)
	_, err = svc.ApplyAntiDegradeMode(context.Background(), a.ID, AntiDegradeMode1)
	require.ErrorContains(t, err, "快照缺失")
	require.Equal(t, 2, mode1Marker(a)["policy_version"])
}
