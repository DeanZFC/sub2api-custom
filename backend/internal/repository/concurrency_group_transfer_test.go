package repository

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestGroupTransferKeepsGlobalSlotAndEnforcesSharedTarget(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	ctx := context.Background()
	ok, err := cache.AcquireUserGroupSlot(ctx, 1, 10, 3, 5, "primary")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.AcquireUserGroupSlot(ctx, 1, 20, 3, 1, "direct")
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = cache.MoveUserGroupSlot(ctx, 1, 10, 20, 3, 1, "primary")
	require.NoError(t, err)
	require.False(t, ok)
	require.EqualValues(t, 1, client.ZCard(ctx, userGroupSlotKey(1, 10)).Val())
	require.NoError(t, cache.ReleaseUserGroupSlot(ctx, 1, 20, "direct"))
	ok, err = cache.MoveUserGroupSlot(ctx, 1, 10, 20, 1, 1, "primary")
	require.NoError(t, err)
	require.True(t, ok, "must not count the same global slot twice")
	require.Zero(t, client.ZCard(ctx, userGroupSlotKey(1, 10)).Val())
	require.EqualValues(t, 1, client.ZCard(ctx, userSlotKey(1)).Val())
	require.EqualValues(t, 1, client.ZCard(ctx, userGroupSlotKey(1, 20)).Val())
	ok, err = cache.AcquireUserGroupSlot(ctx, 1, 20, 10, 1, "blocked")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, cache.ReleaseUserGroupSlot(ctx, 1, 20, "primary"))
	require.Zero(t, client.ZCard(ctx, userSlotKey(1)).Val())
}

func TestGroupTransferConcurrentFallbackOnlyOneWins(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	ctx := context.Background()
	for _, id := range []string{"one", "two"} {
		ok, err := cache.AcquireUserGroupSlot(ctx, 1, 10, 5, 5, id)
		require.NoError(t, err)
		require.True(t, ok)
	}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for _, id := range []string{"one", "two"} {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			ok, err := cache.MoveUserGroupSlot(ctx, 1, 10, 20, 5, 1, id)
			require.NoError(t, err)
			if ok {
				wins.Add(1)
			}
		}(id)
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
	require.EqualValues(t, 2, client.ZCard(ctx, userSlotKey(1)).Val())
	require.EqualValues(t, 1, client.ZCard(ctx, userGroupSlotKey(1, 10)).Val())
	require.EqualValues(t, 1, client.ZCard(ctx, userGroupSlotKey(1, 20)).Val())
}

func TestFallbackConcurrencyPrimaryReleaseIsIdempotent(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { require.NoError(t, client.Close()) })
	cache, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok)
	svc := service.NewConcurrencyService(cache)
	for _, limited := range []bool{false, true} {
		primary, target := int64(10), int64(20)
		key := &service.APIKey{GroupID: &primary, FallbackGroupID: &target, Group: &service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true}, FallbackGroup: &service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, UserConcurrencyLimit: 1}}
		ctx := service.WithAPIKeyGroupFallbackRouting(context.Background(), key)
		max := 0
		if limited {
			max = 2
		}
		lease, err := svc.AcquireUserGroupSlot(ctx, 1, 10, 2, max)
		require.NoError(t, err)
		require.True(t, lease.Acquired)
		lease.ReleaseFunc()
		lease.ReleaseFunc()
		require.Zero(t, client.ZCard(ctx, userSlotKey(1)).Val())
	}
}
