package service

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func protectedPoolKeyTestAccount(mode AntiDegradeMode, tls string) *Account {
	return &Account{
		ID:       901,
		Platform: PlatformOpenAI,
		Type:     AccountTypeOAuth,
		Extra: map[string]any{
			AntiDegradeMarkerExtraKey: map[string]any{
				"enabled":        true,
				"mode":           string(mode),
				"policy_version": 3,
			},
			"tls_fingerprint_builtin": tls,
		},
	}
}

func TestOpenAIWSPoolCompatibilityChangesWithProtectionStrategy(t *testing.T) {
	headers := http.Header{"Session-Id": []string{"session-a"}}
	account := protectedPoolKeyTestAccount(AntiDegradeMode1, "")
	mode1 := normalizeOpenAIWSHandshakeCompatibility(account, headers)
	account.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["mode"] = string(AntiDegradeModeSingleMachine)
	singleMachine := normalizeOpenAIWSHandshakeCompatibility(account, headers)
	require.NotEqual(t, mode1, singleMachine)
}

func TestOpenAIWSPoolCompatibilityChangesWithProtectionTLS(t *testing.T) {
	headers := http.Header{"Session-Id": []string{"session-a"}}
	account := protectedPoolKeyTestAccount(AntiDegradeMode1, "nodejs24")
	first := normalizeOpenAIWSHandshakeCompatibility(account, headers)
	account.Extra["tls_fingerprint_builtin"] = "nodejs22"
	second := normalizeOpenAIWSHandshakeCompatibility(account, headers)
	require.NotEqual(t, first, second)
}

func TestOpenAIWSPoolCompatibilityIgnoresProtectionFieldsWhenDisabled(t *testing.T) {
	headers := http.Header{"Session-Id": []string{"session-a"}}
	account := protectedPoolKeyTestAccount(AntiDegradeMode1, "nodejs24")
	account.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["enabled"] = false
	first := normalizeOpenAIWSHandshakeCompatibility(account, headers)
	account.Extra[AntiDegradeMarkerExtraKey].(map[string]any)["mode"] = string(AntiDegradeModeSingleMachine)
	account.Extra["tls_fingerprint_builtin"] = "nodejs22"
	second := normalizeOpenAIWSHandshakeCompatibility(account, headers)
	require.Equal(t, first, second)
}
