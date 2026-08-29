package dto

import (
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountFromServiceShallowMapsRateLimit429RetryCount(t *testing.T) {
	t.Run("missing legacy value uses default", func(t *testing.T) {
		got := AccountFromServiceShallow(&service.Account{})
		require.Equal(t, service.DefaultRateLimit429RetryCount, got.RateLimit429RetryCount)
	})

	t.Run("explicit zero remains disabled", func(t *testing.T) {
		zero := 0
		got := AccountFromServiceShallow(&service.Account{RateLimit429RetryCount: &zero})
		require.Zero(t, got.RateLimit429RetryCount)
	})
}
