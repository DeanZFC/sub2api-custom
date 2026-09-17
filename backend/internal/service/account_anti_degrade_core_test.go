package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func newAntiDegradeTestService(account *Account) (*AntiDegradeService, *upstreamBillingProbeAccountRepo) {
	repo := &upstreamBillingProbeAccountRepo{accounts: map[int64]*Account{account.ID: account}}
	return NewAntiDegradeService(&adminServiceImpl{accountRepo: repo}), repo
}

func TestAntiDegradeMode1ApplyProtectsIdentityAndSnapshot(t *testing.T) {
	account := &Account{ID: 8101, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{
		codexFingerprintModeExtraKey: string(codexFingerprintOff),
		"custom_setting":             "preserve",
	}}
	svc, repo := newAntiDegradeTestService(account)

	updated, err := svc.ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeMode1)
	require.NoError(t, err)
	require.Equal(t, string(AntiDegradeMode1), updated.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["mode"])
	require.Equal(t, 3, updated.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["policy_version"])
	require.Equal(t, "device", updated.Extra[codexFingerprintModeExtraKey])
	require.Equal(t, false, updated.Extra["enable_tls_fingerprint"])
	require.Equal(t, "preserve", updated.Extra["custom_setting"])
	require.Equal(t, 3, updated.Concurrency, "the administrator's configured ceiling is preserved")
	_, ok := codexFingerprintSeed(updated.Extra)
	require.True(t, ok, "applying a device strategy must mint a durable account seed")
	// Identity/transport protection and adaptive traffic control are separate
	// switches. Applying a strategy must not silently enable the latter.
	_, policyPresent := updated.Extra[AccountProtectionPolicyKey]
	require.False(t, policyPresent)
	require.Len(t, repo.accounts, 1)
}

func TestAntiDegradeOrdinaryAccountSaveCannotDisableProtection(t *testing.T) {
	account := &Account{ID: 8102, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{}}
	svc, _ := newAntiDegradeTestService(account)
	_, err := svc.ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeMode1)
	require.NoError(t, err)

	admin := svc.admin.(*adminServiceImpl)
	updated, err := admin.UpdateAccount(context.Background(), account.ID, &UpdateAccountInput{Extra: map[string]any{"custom_setting": "new"}})
	require.NoError(t, err)
	require.True(t, updated.AntiDegradationEnabled())
	require.Equal(t, string(AntiDegradeMode1), updated.ProtectionMode())
	require.Equal(t, "device", updated.Extra[codexFingerprintModeExtraKey])
	require.Equal(t, "new", updated.Extra["custom_setting"])
}

func TestAntiDegradeRevertRestoresOriginalConfiguration(t *testing.T) {
	account := &Account{ID: 8103, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{
		codexFingerprintModeExtraKey: "session",
		"enable_tls_fingerprint":     true,
		"tls_fingerprint_builtin":    "nodejs24",
		AccountProtectionPolicyKey:   map[string]any{"enabled": false},
	}}
	svc, _ := newAntiDegradeTestService(account)
	_, err := svc.ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeMode1)
	require.NoError(t, err)
	updated, err := svc.Revert(context.Background(), account.ID)
	require.NoError(t, err)
	require.False(t, updated.AntiDegradationEnabled())
	require.Equal(t, "session", updated.Extra[codexFingerprintModeExtraKey])
	require.Equal(t, true, updated.Extra["enable_tls_fingerprint"])
	require.Equal(t, "nodejs24", updated.Extra["tls_fingerprint_builtin"])
	require.Equal(t, map[string]any{"enabled": false}, updated.Extra[AccountProtectionPolicyKey])
}

func TestAntiDegradeSwitchKeepsFirstBaselineSnapshot(t *testing.T) {
	account := &Account{ID: 8104, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{
		codexFingerprintModeExtraKey: "off",
	}}
	svc, _ := newAntiDegradeTestService(account)
	_, err := svc.ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeMode1)
	require.NoError(t, err)
	_, err = svc.ApplyAntiDegradeMode(context.Background(), account.ID, AntiDegradeModeLegacy)
	require.NoError(t, err)
	updated, err := svc.Revert(context.Background(), account.ID)
	require.NoError(t, err)
	require.False(t, updated.AntiDegradationEnabled())
	require.Equal(t, "off", updated.Extra[codexFingerprintModeExtraKey])
}
