package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGatewayCacheSessionAccountCompareAndSet(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	cache, ok := NewGatewayCache(client).(service.SessionAccountCompareAndSetter)
	require.True(t, ok)
	ctx := context.Background()
	changed, err := cache.CompareAndSetSessionAccountID(ctx, 43, "user-affinity", 0, 1, time.Hour)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = cache.CompareAndSetSessionAccountID(ctx, 43, "user-affinity", 0, 2, time.Hour)
	require.NoError(t, err)
	require.False(t, changed)
	changed, err = cache.CompareAndSetSessionAccountID(ctx, 43, "user-affinity", 1, 2, 24*time.Hour)
	require.NoError(t, err)
	require.True(t, changed)
	changed, err = cache.CompareAndSetSessionAccountID(ctx, 43, "user-affinity", 1, 1, time.Hour)
	require.NoError(t, err)
	require.False(t, changed)
	value, err := server.Get(buildSessionKey(43, "user-affinity"))
	require.NoError(t, err)
	require.Equal(t, "2", value)
	require.Equal(t, 24*time.Hour, server.TTL(buildSessionKey(43, "user-affinity")))
	changed, err = cache.CompareAndSetSessionAccountID(ctx, 44, "user-affinity", 0, 3, time.Hour)
	require.NoError(t, err)
	require.True(t, changed)
	var winners atomic.Int32
	var wg sync.WaitGroup
	errs := make(chan error, 20)
	for id := int64(1); id <= 20; id++ {
		wg.Add(1)
		go func(accountID int64) {
			defer wg.Done()
			ok, err := cache.CompareAndSetSessionAccountID(ctx, 43, "concurrent-first", 0, accountID, time.Hour)
			if ok {
				winners.Add(1)
			}
			errs <- err
		}(id)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, int32(1), winners.Load())
}
