package service

import (
	"encoding/json"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAccountProtectionIdentityAliasesStayConsistentAcrossTransports(t *testing.T) {
	for _, mode := range []AntiDegradeMode{
		AntiDegradeModeLegacy, AntiDegradeMode1, AntiDegradeMode2,
		AntiDegradeModeMinimal, AntiDegradeModeSession, AntiDegradeModeTLSNode24,
		AntiDegradeModeLowConcurrency, AntiDegradeModeSingleMachine,
	} {
		t.Run(string(mode), func(t *testing.T) {
			identity, _, _ := antiDegradeSettings(mode)
			marker := map[string]any{"enabled": true, "mode": string(mode)}
			if mode == AntiDegradeMode1 {
				marker["policy_version"] = mode1PolicyVersion
			}
			account := newTestOAuthAccount(8301, map[string]any{
				codexFingerprintModeExtraKey: string(identity),
				AntiDegradationExtraKey:      true,
				AntiDegradeMarkerExtraKey:    marker,
			})
			ids := resolveCodexFingerprintIDs(account, "caller-session", identity)
			require.NotNil(t, ids)
			body := []byte(`{"input":[{"role":"user","content":"Keep this prompt"}],"prompt_cache_key":"application-cache","client_metadata":{"installation_id":"caller-device","session_id":"caller-session","session-id":"caller-session","thread_id":"caller-thread","thread-id":"caller-thread","turn_id":"caller-turn","turn-id":"caller-turn","window_id":"caller-window","x-client-request-id":"caller-request","trace":"keep"}}`)
			mapped, raw := applyMapAndRawFingerprintBodiesForTest(t, body, ids)
			require.Equal(t, mapped, raw, "HTTP and raw JSON paths must project the same identity")
			metadata := raw["client_metadata"].(map[string]any)
			headers := make(http.Header)
			applyCodexFingerprintHeaders(headers, ids)
			require.Equal(t, headers.Get("x-codex-installation-id"), metadata["installation_id"])
			require.Equal(t, metadata["x-codex-installation-id"], metadata["installation_id"])
			require.Equal(t, "keep", metadata["trace"])
			require.Equal(t, "application-cache", raw["prompt_cache_key"])
			require.Equal(t, []any{map[string]any{"role": "user", "content": "Keep this prompt"}}, raw["input"])
			if identity == codexFingerprintDevice {
				require.Equal(t, "caller-session", metadata["session-id"], "device protection preserves separate sessions")
				require.Equal(t, "caller-thread", metadata["thread-id"])
				require.Equal(t, "caller-turn", metadata["turn-id"])
				require.Equal(t, "caller-window", metadata["window_id"])
				require.Equal(t, "caller-request", metadata["x-client-request-id"])
				return
			}
			require.Equal(t, headers.Get("session-id"), metadata["session-id"])
			require.Equal(t, metadata["session_id"], metadata["session-id"])
			require.Equal(t, headers.Get("thread-id"), metadata["thread-id"])
			require.Equal(t, metadata["thread_id"], metadata["thread-id"])
			require.Equal(t, metadata["turn_id"], metadata["turn-id"])
			require.Equal(t, headers.Get("x-codex-window-id"), metadata["window_id"])
			require.Equal(t, metadata["x-codex-window-id"], metadata["window_id"])
			require.Equal(t, headers.Get("x-client-request-id"), metadata["x-client-request-id"])
		})
	}
}

func TestAccountProtectionIdentityAliasesKeepDisabledBehavior(t *testing.T) {
	for _, identity := range []codexFingerprintMode{codexFingerprintDevice, codexFingerprintSession, codexFingerprintFull, codexFingerprintSingleMachineMultiWindow} {
		t.Run(string(identity), func(t *testing.T) {
			account := newTestOAuthAccount(8302, map[string]any{
				codexFingerprintModeExtraKey: string(identity),
				AntiDegradationExtraKey:      false,
			})
			ids := resolveCodexFingerprintIDs(account, "caller-session", identity)
			require.NotNil(t, ids)
			body := []byte(`{"client_metadata":{"installation_id":"caller-device","session-id":"caller-session","thread-id":"caller-thread","turn-id":"caller-turn","window_id":"caller-window","x-client-request-id":"caller-request"}}`)
			var original map[string]any
			require.NoError(t, json.Unmarshal(body, &original))
			mapped, raw := applyMapAndRawFingerprintBodiesForTest(t, body, ids)
			require.Equal(t, mapped, raw)
			metadata := raw["client_metadata"].(map[string]any)
			for key, value := range original["client_metadata"].(map[string]any) {
				require.Equal(t, value, metadata[key], "disabled protection must preserve the existing alias behavior for %s", key)
			}
		})
	}
}

func TestAccountProtectionIdentityAliasesAreNotInvented(t *testing.T) {
	account := newTestOAuthAccount(8303, map[string]any{
		codexFingerprintModeExtraKey: "session",
		AntiDegradationExtraKey:      true,
		AntiDegradeMarkerExtraKey:    map[string]any{"enabled": true, "mode": string(AntiDegradeModeLegacy)},
	})
	ids := resolveCodexFingerprintIDs(account, "caller-session", codexFingerprintSession)
	require.NotNil(t, ids)
	mapped, raw := applyMapAndRawFingerprintBodiesForTest(t, []byte(`{"client_metadata":{"trace":"keep"}}`), ids)
	require.Equal(t, mapped, raw)
	metadata := raw["client_metadata"].(map[string]any)
	for _, key := range []string{"installation_id", "session-id", "thread-id", "turn-id", "window_id", "x-client-request-id"} {
		require.NotContains(t, metadata, key)
	}
}
