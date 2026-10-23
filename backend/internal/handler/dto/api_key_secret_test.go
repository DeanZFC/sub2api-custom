package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAPIKeySecretDefaultAndOneTimeSerialization(t *testing.T) {
	const raw = "sk-secret-once-123456789abcdef"
	for _, stored := range []string{raw, service.APIKeyStoredValue(raw)} {
		key := &service.APIKey{ID: 7, Key: stored}
		masked := APIKeyFromService(key)
		require.Equal(t, "sk-sec...cdef", masked.Key)
		for _, v := range []any{masked, UserFromService(&service.User{APIKeys: []service.APIKey{*key}}), UsageLogFromService(&service.UsageLog{APIKey: key})} {
			body, err := json.Marshal(v)
			require.NoError(t, err)
			require.NotContains(t, string(body), raw)
			require.NotContains(t, string(body), "$sha256$")
		}
	}
	secret := &APIKeySecret{APIKey: *APIKeyFromService(&service.APIKey{ID: 7, Key: raw}), Key: raw}
	first, err := json.Marshal(secret)
	require.NoError(t, err)
	require.Contains(t, string(first), raw)
	safe, err := json.Marshal(secret.IdempotencyStoredResponse())
	require.NoError(t, err)
	require.NotContains(t, string(safe), raw)
	require.Contains(t, string(safe), "sk-sec...cdef")
}
