package repository

import (
	"context"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

// newDedicatedAPIKeyTTLCache builds a cache whose shared slot TTL is 15 minutes
// while ordinary API key slots use the dedicated short TTL, mirroring the
// intended 120-second deployment on a smaller, test-friendly scale. The virtual
// clock starts at a fixed point so deadlines and scores stay deterministic.
func newDedicatedAPIKeyTTLCache(t *testing.T, apiKeyTTLSeconds int) (*concurrencyCache, *miniredis.Miniredis) {
	t.Helper()
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })
	server.SetTime(time.Unix(1_800_000_000, 0))
	cache, ok := NewConcurrencyCacheWithAPIKeyTTL(client, 15, 900, apiKeyTTLSeconds).(*concurrencyCache)
	require.True(t, ok, "expected Redis concurrency cache")
	return cache, server
}

func TestConcurrencyCacheAPIKeyTTLOverrideAndInherit(t *testing.T) {
	server := miniredis.RunT(t)
	client := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() { _ = client.Close() })

	// The legacy three-argument constructor keeps inheriting the shared TTL.
	legacy, ok := NewConcurrencyCache(client, 15, 900).(*concurrencyCache)
	require.True(t, ok, "expected Redis concurrency cache")
	require.Equal(t, 15*time.Minute, legacy.APIKeySlotTTL())
	require.Equal(t, 5*time.Minute, legacy.APIKeySlotRefreshInterval())

	// A dedicated positive value overrides only the API key TTL.
	dedicated, ok := NewConcurrencyCacheWithAPIKeyTTL(client, 15, 900, 120).(*concurrencyCache)
	require.True(t, ok, "expected Redis concurrency cache")
	require.Equal(t, 120*time.Second, dedicated.APIKeySlotTTL())
	require.Equal(t, 40*time.Second, dedicated.APIKeySlotRefreshInterval())
	require.Equal(t, 900, dedicated.slotTTLSeconds, "shared slot TTL must stay untouched")
	require.Equal(t, 900, dedicated.waitQueueTTLSeconds)

	// Non-positive dedicated values inherit, including the zero-value struct
	// literal shape used by existing tests and callers.
	negative, ok := NewConcurrencyCacheWithAPIKeyTTL(client, 15, 900, -5).(*concurrencyCache)
	require.True(t, ok, "expected Redis concurrency cache")
	require.Equal(t, 15*time.Minute, negative.APIKeySlotTTL(), "negative dedicated TTL must inherit")
	literal := &concurrencyCache{rdb: client, slotTTLSeconds: 900}
	require.Equal(t, 15*time.Minute, literal.APIKeySlotTTL(), "zero-value struct literal must inherit")
}

func TestAPIKeySlotDedicatedTTLSharedClientsKeepLivePeer(t *testing.T) {
	server := miniredis.RunT(t)
	clientA := redis.NewClient(&redis.Options{Addr: server.Addr()})
	clientB := redis.NewClient(&redis.Options{Addr: server.Addr()})
	t.Cleanup(func() {
		_ = clientA.Close()
		_ = clientB.Close()
	})
	now := time.Unix(1_800_000_000, 0)
	server.SetTime(now)
	cacheA, ok := NewConcurrencyCacheWithAPIKeyTTL(clientA, 15, 900, 30).(*concurrencyCache)
	require.True(t, ok, "expected Redis concurrency cache")
	cacheB, ok := NewConcurrencyCacheWithAPIKeyTTL(clientB, 15, 900, 30).(*concurrencyCache)
	require.True(t, ok, "expected Redis concurrency cache")
	ctx := context.Background()

	require.NoError(t, cacheA.TrackAPIKeySlot(ctx, 9, "live"))
	require.NoError(t, cacheA.TrackAPIKeySlot(ctx, 9, "dead"))

	// A second Redis client renews the live peer after the dead member's score
	// has aged out; only the dead member should be pruned. miniredis
	// FastForward only decrements TTLs, so advance its TIME separately.
	now = now.Add(15 * time.Second)
	server.FastForward(15 * time.Second)
	server.SetTime(now)
	ok, err := cacheB.RefreshAPIKeySlot(ctx, 9, "live")
	require.NoError(t, err)
	require.True(t, ok)
	now = now.Add(16 * time.Second)
	server.FastForward(16 * time.Second)
	server.SetTime(now)

	counts, err := cacheA.GetAPIKeyConcurrencyBatch(ctx, []int64{9})
	require.NoError(t, err)
	require.Equal(t, 1, counts[9], "live peer survives while the dead peer expires")

	ok, err = cacheB.RefreshAPIKeySlot(ctx, 9, "dead")
	require.NoError(t, err)
	require.False(t, ok, "late refresh from another client must not recreate the dead member")

	// A joining Pod's startup cleanup must not delete a live API key peer.
	require.NoError(t, cacheB.CleanupStaleProcessSlots(ctx, "joining-pod-"))
	counts, err = cacheA.GetAPIKeyConcurrencyBatch(ctx, []int64{9})
	require.NoError(t, err)
	require.Equal(t, 1, counts[9])
}

func TestAPIKeySlotDedicatedTTLSparesAccountAndUserSlots(t *testing.T) {
	cache, server := newDedicatedAPIKeyTTLCache(t, 30)
	ctx := context.Background()

	acquired, err := cache.AcquireAccountSlot(ctx, 11, 1, "acct")
	require.NoError(t, err)
	require.True(t, acquired)
	acquired, err = cache.AcquireUserSlot(ctx, 12, 1, "usr")
	require.NoError(t, err)
	require.True(t, acquired)
	require.NoError(t, cache.TrackAPIKeySlot(ctx, 13, "key"))

	// Older than the dedicated API key TTL, still far inside the 15-minute
	// shared slot TTL that account/user members keep.
	server.FastForward(31 * time.Second)

	accountCount, err := cache.GetAccountConcurrency(ctx, 11)
	require.NoError(t, err)
	require.Equal(t, 1, accountCount, "account slot keeps the shared TTL")
	userCount, err := cache.GetUserConcurrency(ctx, 12)
	require.NoError(t, err)
	require.Equal(t, 1, userCount, "user slot keeps the shared TTL")

	counts, err := cache.GetAPIKeyConcurrencyBatch(ctx, []int64{13})
	require.NoError(t, err)
	require.Zero(t, counts[13], "API key stats-only member expires with the dedicated TTL")
}
