//go:build unit

package repository

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCachePreservesRateLimit429RetryCount(t *testing.T) {
	zero := 0
	account := service.Account{
		ID:                     429,
		Name:                   "429-retry-disabled",
		Platform:               service.PlatformOpenAI,
		Type:                   service.AccountTypeAPIKey,
		Status:                 service.StatusActive,
		Schedulable:            true,
		RateLimit429RetryCount: &zero,
	}

	metadata := buildSchedulerMetadataAccount(account)
	require.NotNil(t, metadata.RateLimit429RetryCount)
	require.Zero(t, *metadata.RateLimit429RetryCount)

	fullPayload, metadataPayload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)
	for name, payload := range map[string][]byte{"full": fullPayload, "metadata": metadataPayload} {
		decoded, decodeErr := decodeCachedAccount(payload)
		require.NoError(t, decodeErr, name)
		require.NotNil(t, decoded.RateLimit429RetryCount, "%s payload must preserve explicit zero", name)
		require.Zero(t, *decoded.RateLimit429RetryCount, name)
	}
}
