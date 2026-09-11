package admin

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestParseCodexCredentialsForSharedImport(t *testing.T) {
	token := buildCodexAccessToken(t, "account", "user", time.Now().Add(time.Hour))
	content, _ := json.Marshal(map[string]any{"tokens": map[string]any{"access_token": token, "refresh_token": "refresh-test"}})
	entries, err := ParseCodexCredentials(string(content), "shared")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Empty(t, entries[0].Error)
	require.Equal(t, token, entries[0].Credentials["access_token"])
	require.Equal(t, "refresh-test", entries[0].Credentials["refresh_token"])
	require.Equal(t, "account", entries[0].Credentials["chatgpt_account_id"])
	require.Nil(t, entries[0].ExpiresAt, "refreshable accounts must not expire with their access token")
	entries, err = ParseCodexCredentials(token, "shared")
	require.NoError(t, err)
	require.Empty(t, entries[0].Error)
	require.NotNil(t, entries[0].ExpiresAt, "access-only imports must stop at their token expiry")
	entries, err = ParseCodexCredentials("opaque-token-without-expiry", "shared")
	require.NoError(t, err)
	require.NotEmpty(t, entries[0].Error)
}

func TestParseCodexCredentialsPreservesAgentIdentityWithoutAccessToken(t *testing.T) {
	value := buildAgentIdentityImportValue(t, "runtime-shared", "account-shared", "user-shared", "task-shared")
	content, err := json.Marshal(value)
	require.NoError(t, err)
	entries, err := ParseCodexCredentials(string(content), "shared")
	require.NoError(t, err)
	require.Len(t, entries, 1)
	require.Empty(t, entries[0].Error)
	require.Equal(t, "runtime-shared", entries[0].Credentials["agent_runtime_id"])
	require.Equal(t, "task-shared", entries[0].Credentials["task_id"])
	require.NotEmpty(t, entries[0].Credentials["agent_private_key"])
	require.NotContains(t, entries[0].Credentials, "access_token")
	require.Nil(t, entries[0].ExpiresAt)
}
