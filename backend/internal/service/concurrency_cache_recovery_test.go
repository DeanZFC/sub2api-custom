package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type cacheRecoveryGateFunc func(context.Context, int64) (bool, error)

func (f cacheRecoveryGateFunc) AcquireCacheRecoveryRequest(ctx context.Context, accountID int64) (bool, error) {
	return f(ctx, accountID)
}

func TestCacheRecoveryAdmissionChecksUnlimitedAndReleasesRejectedSlots(t *testing.T) {
	for _, limit := range []int{0, -1, 3} {
		for _, gateErr := range []error{nil, errors.New("admission database unavailable")} {
			cache := &proxyLegacyCache{acquireResult: true}
			svc := NewConcurrencyService(cache)
			calls := 0
			svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(_ context.Context, accountID int64) (bool, error) {
				calls++
				require.Equal(t, int64(7), accountID)
				return false, gateErr
			}))
			result, err := svc.AcquireAccountSlot(context.Background(), 7, limit)
			require.ErrorIs(t, err, gateErr)
			require.False(t, result.Acquired)
			require.Nil(t, result.ReleaseFunc)
			require.Equal(t, 1, calls)
			if limit > 0 {
				require.Equal(t, []int64{7}, cache.releasedAccountIDs)
			} else {
				require.Empty(t, cache.releasedAccountIDs)
			}
		}
	}
}

func TestCacheRecoveryAdmissionDoesNotSpendBudgetWhileSaturated(t *testing.T) {
	cache := &proxyLegacyCache{}
	svc := NewConcurrencyService(cache)
	svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) {
		t.Fatal("saturated concurrency must not consume a trial request")
		return false, nil
	}))
	for range 3 {
		result, err := svc.AcquireAccountSlot(context.Background(), 7, 1)
		require.NoError(t, err)
		require.False(t, result.Acquired)
	}
	require.Empty(t, cache.releasedAccountIDs)
}

func TestCacheRecoveryAdmissionChecksEachProxyAndWebSocketTurn(t *testing.T) {
	for _, limit := range []int{0, 2} {
		cache := &routingTestCache{proxyLegacyCache: &proxyLegacyCache{}, selected: 22}
		svc := NewConcurrencyService(cache)
		account := proxyRoutingAccount()
		calls := 0
		svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(_ context.Context, id int64) (bool, error) {
			require.Equal(t, account.ID, id)
			calls++
			return calls == 1, nil
		}))
		first, err := svc.AcquireAccountRoute(context.Background(), &account, limit)
		require.NoError(t, err)
		require.True(t, first.Acquired)
		require.Equal(t, int64(22), account.SelectedProxyID)
		first.ReleaseFunc()
		cache.released = 0
		second, err := svc.AcquireAccountRoute(context.Background(), &account, limit)
		require.NoError(t, err)
		require.False(t, second.Acquired)
		require.Equal(t, 2, calls, "a pinned later turn still needs its own admission")
		require.Equal(t, int64(22), cache.pinned)
		require.Equal(t, int64(22), cache.released)
	}
}

func TestCacheRecoveryAdmissionProxyErrorAndSaturation(t *testing.T) {
	cache := &routingTestCache{proxyLegacyCache: &proxyLegacyCache{}}
	svc := NewConcurrencyService(cache)
	calls := 0
	svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) {
		calls++
		return false, errors.New("admission database unavailable")
	}))
	result, err := svc.AcquireAccountSlot(context.Background(), 7, 1, 11, 22)
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Zero(t, calls)
	cache.selected = 11
	result, err = svc.AcquireAccountSlot(context.Background(), 7, 1, 11, 22)
	require.ErrorContains(t, err, "admission database unavailable")
	require.False(t, result.Acquired)
	require.Equal(t, int64(11), cache.released)
	require.Equal(t, 1, calls)
}
