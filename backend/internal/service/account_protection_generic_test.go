package service

import (
	"context"
	"maps"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestAntiDegradeGenericDefaultPreservesNativeConfiguration(t *testing.T) {
	parentID := int64(99)
	for _, tc := range []struct {
		name, platform, kind string
		parent               *int64
		proxies              []int64
		random               bool
	}{
		{name: "OpenAI API key", platform: PlatformOpenAI, kind: AccountTypeAPIKey},
		{name: "Anthropic API key", platform: PlatformAnthropic, kind: AccountTypeAPIKey},
		{name: "Gemini OAuth", platform: PlatformGemini, kind: AccountTypeOAuth},
		{name: "OpenAI shadow", platform: PlatformOpenAI, kind: AccountTypeOAuth, parent: &parentID},
		{name: "OpenAI multiple proxies", platform: PlatformOpenAI, kind: AccountTypeOAuth, proxies: []int64{4, 5}},
		{name: "Anthropic random proxy", platform: PlatformAnthropic, kind: AccountTypeSetupToken, random: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			extra := map[string]any{"codex_fingerprint_mode": "off", "enable_tls_fingerprint": true, "tls_fingerprint_profile_id": int64(17), "custom_setting": "keep"}
			if tc.random {
				extra["proxy_mode"] = "random"
			}
			originalExtra := maps.Clone(extra)
			a := &Account{ID: 8601, Platform: tc.platform, Type: tc.kind, ParentAccountID: tc.parent, ProxyIDs: tc.proxies, Concurrency: 0, Extra: extra}
			svc, _ := newAntiDegradeTestService(a)
			preview, err := svc.Preview(context.Background(), a.ID)
			require.NoError(t, err)
			for _, change := range preview.Changes {
				require.Contains(t, []string{"strategy", "concurrency"}, change.Key)
			}
			updated, err := svc.Apply(context.Background(), a.ID)
			require.NoError(t, err)
			require.Equal(t, "generic", updated.ProtectionMode())
			require.Equal(t, "generic_v1", updated.ProtectionScope())
			require.False(t, updated.IdentityProtectionEnabled())
			require.Equal(t, 16, updated.Concurrency)
			require.Equal(t, tc.proxies, updated.ProxyIDs)
			for key, value := range originalExtra {
				require.Equal(t, value, updated.Extra[key], key)
			}
			require.Equal(t, map[string]any{"concurrency": 0}, antiDegradePrevious(updated))
			require.NoError(t, ValidateAccountProtectionConfiguration(updated))
			require.NotContains(t, updated.Extra, codexFingerprintSeedExtraKey)
			require.NotContains(t, updated.Extra, AccountProtectionPolicyKey)
			require.False(t, ProtectedProxyModeConflict(updated, map[string]any{"proxy_mode": "random"}))
			require.False(t, ProtectedProxyPoolConflict(updated, []int64{6, 7}))
			updated, err = svc.Revert(context.Background(), a.ID)
			require.NoError(t, err)
			require.Equal(t, 0, updated.Concurrency)
			for key, value := range originalExtra {
				require.Equal(t, value, updated.Extra[key], key)
			}
		})
	}
}

func TestAntiDegradeGenericRevertKeepsLaterNativeEdits(t *testing.T) {
	a := &Account{ID: 8602, Platform: PlatformOpenAI, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{"codex_fingerprint_mode": "off"}}
	svc, repo := newAntiDegradeTestService(a)
	updated, err := svc.ApplyAntiDegradeMode(context.Background(), a.ID, AntiDegradeModeGeneric)
	require.NoError(t, err)
	require.Equal(t, 3, updated.Concurrency)
	updated.Extra["codex_fingerprint_mode"] = "session"
	updated.Extra["tls_fingerprint_builtin"] = "original-native-profile"
	updated.Extra["proxy_mode"] = "random"
	updated.Concurrency = 5
	BoundAccountProtectionConcurrency(updated)
	repo.accounts[a.ID] = updated
	require.Equal(t, []string{AntiDegradeMarkerExtraKey, AntiDegradationExtraKey, ProtectionScopeExtraKey}, ProtectionManagedKeys(updated))
	updated, err = svc.Revert(context.Background(), a.ID)
	require.NoError(t, err)
	require.Equal(t, 5, updated.Concurrency)
	require.Equal(t, "session", updated.Extra["codex_fingerprint_mode"])
	require.Equal(t, "original-native-profile", updated.Extra["tls_fingerprint_builtin"])
	require.Equal(t, "random", updated.Extra["proxy_mode"])
}

func TestAntiDegradeGenericKeepsNativeTransportAndProbe(t *testing.T) {
	a := &Account{ID: 8603, Platform: PlatformAnthropic, Type: AccountTypeOAuth, Concurrency: 3, Extra: map[string]any{"enable_tls_fingerprint": true}}
	tlsService := &TLSFingerprintProfileService{}
	before := tlsService.ResolveTLSProfile(a)
	svc, _ := newAntiDegradeTestService(a)
	updated, err := svc.ApplyAntiDegradeMode(context.Background(), a.ID, AntiDegradeModeGeneric)
	require.NoError(t, err)
	require.Equal(t, before, tlsService.ResolveTLSProfile(updated))
	profile, err := resolveProtectionTransport(updated, nil)
	require.NoError(t, err)
	require.Nil(t, profile, "generic must not supply a replacement transport")
	updated.Platform = PlatformOpenAI
	require.Equal(t, "off", updated.RequestIntegrityMode())
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	raw := []byte("{ \"input\": \"same bytes\" }\n")
	got, err := prepareIntelligentTestProtectionBytes(c, updated, raw)
	require.NoError(t, err)
	require.Equal(t, raw, got)
}
