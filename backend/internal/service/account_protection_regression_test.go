package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAntiDegradeDefaultMatchesImportedLegacyPolicy(t *testing.T) {
	for _, kind := range []string{AccountTypeOAuth, AccountTypeSetupToken} {
		for _, platform := range []string{PlatformOpenAI, PlatformAnthropic} {
			t.Run(platform+"/"+kind, func(t *testing.T) {
				a := &Account{ID: 8410, Platform: platform, Type: kind, Concurrency: 3}
				svc, _ := newAntiDegradeTestService(a)
				updated, err := svc.SetProtection(context.Background(), a.ID, true, false)
				require.NoError(t, err)
				require.Equal(t, "legacy", updated.ProtectionMode())
				require.Equal(t, 3, updated.Concurrency)
				require.Equal(t, "nodejs24", updated.Extra["tls_fingerprint_builtin"])
				require.Equal(t, true, updated.Extra["enable_tls_fingerprint"])
				require.NotContains(t, updated.Extra, AccountProtectionPolicyKey)
				if platform == PlatformOpenAI {
					require.Equal(t, codexFingerprintSession, updated.GetCodexFingerprintMode())
					_, ok := codexFingerprintSeed(updated.Extra)
					require.True(t, ok)
				}
				require.NoError(t, ValidateAccountProtectionConfiguration(updated))
			})
		}
	}
}

func TestAntiDegradeRevertPreservesEditedConcurrency(t *testing.T) {
	a := &Account{ID: 8411, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3}
	svc, repo := newAntiDegradeTestService(a)
	updated, err := svc.Apply(context.Background(), a.ID)
	require.NoError(t, err)
	// The repository mirrors every explicit concurrency edit into the runtime
	// marker, as it would when an administrator changes 3 to 5 after enabling.
	updated.Concurrency = 5
	BoundAccountProtectionConcurrency(updated)
	repo.accounts[a.ID] = updated
	require.Equal(t, 5, mode1Marker(updated)["max_concurrency"])
	require.Equal(t, 3, mode1Marker(updated)["applied_concurrency"])
	updated, err = svc.Revert(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, 5, updated.Concurrency)
	require.False(t, updated.AntiDegradationEnabled())
}

func TestAntiDegradeRegisteredStrategiesRejectMismatchedState(t *testing.T) {
	for _, p := range ListAntiDegradeStrategyProfiles() {
		if !p.ApplySupported || p.ID == AntiDegradeModeGeneric {
			continue
		}
		t.Run(string(p.ID), func(t *testing.T) {
			a := &Account{ID: 8412, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3}
			svc, _ := newAntiDegradeTestService(a)
			updated, err := svc.ApplyAntiDegradeMode(context.Background(), a.ID, p.ID)
			require.NoError(t, err)
			require.NoError(t, validateRegisteredAntiDegrade(updated))
			seed := updated.Extra[codexFingerprintSeedExtraKey]
			delete(updated.Extra, codexFingerprintSeedExtraKey)
			require.Error(t, validateRegisteredAntiDegrade(updated))
			updated.Extra[codexFingerprintSeedExtraKey] = seed
			updated.Extra[codexFingerprintModeExtraKey] = "off"
			require.Error(t, validateRegisteredAntiDegrade(updated))
			updated.Extra[codexFingerprintModeExtraKey] = p.IdentityMode
			updated.Extra["tls_fingerprint_builtin"] = "unrecognized-profile"
			require.Error(t, validateRegisteredAntiDegrade(updated))
		})
	}
}

func TestAntiDegradeExplicitDisableOverridesStaleMarker(t *testing.T) {
	a := mode1TestAccount()
	a.Extra[AntiDegradationExtraKey] = false
	require.False(t, a.AntiDegradationEnabled())
	require.Equal(t, "off", a.RequestIntegrityMode())
	require.NoError(t, validateRegisteredAntiDegrade(a))
}

func TestAntiDegradeUnknownMode1VersionFailsBeforeSend(t *testing.T) {
	a := mode1TestAccount()
	mode1Marker(a)["policy_version"] = 99
	require.ErrorContains(t, validateRegisteredAntiDegrade(a), "配置不完整")
}

func TestAntiDegradeMode1DeviceIdentityUsesAccountSeed(t *testing.T) {
	a := mode1TestAccount()
	a.Extra["openai_device_id"] = "shared-imported-device"
	first := resolveConvergedInstallationID(a, "first-account-seed")
	second := resolveConvergedInstallationID(a, "second-account-seed")
	require.NotEqual(t, first, second)
	require.Equal(t, deriveStableUUIDv4("sub2api:mode1-install-id:v2:first-account-seed"), first)
	a.Extra[AntiDegradationExtraKey] = false
	require.Equal(t, "shared-imported-device", resolveConvergedInstallationID(a, "first-account-seed"))
}

func TestAntiDegradeDisabledProbePreservesExactBytes(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	raw := []byte("{ \"z\": 1.0, \"input\": \"<div>\", \"model\": \"test\" }\n")
	for _, a := range []*Account{nil, {Platform: PlatformOpenAI, Type: AccountTypeOAuth}} {
		got, err := prepareIntelligentTestProtectionBytes(c, a, raw)
		require.NoError(t, err)
		require.Equal(t, raw, got)
		require.Same(t, &raw[0], &got[0])
	}
}
