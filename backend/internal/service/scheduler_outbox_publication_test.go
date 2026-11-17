//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type snapshotValuesAuthoritativeRepo struct{ AccountRepository }

func (*snapshotValuesAuthoritativeRepo) WithLatestSchedulerAccounts(context.Context, []int64, func([]Account) error) error {
	panic("snapshot reads must not publish account data")
}

func TestOutboxAuthoritativeSnapshotPreservesValuesReader(t *testing.T) {
	cache := &valuesReaderCache{values: []Account{
		{ID: 7301, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true},
	}}
	svc := newFallbackCoalesceService(cache, &snapshotValuesAuthoritativeRepo{})
	require.IsType(t, &outboxAuthoritativeSchedulerCache{}, svc.cache)
	accounts, _, err := svc.ListSchedulableAccounts(context.Background(), nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Same(t, &cache.values[0], &accounts[0], "the wrapper must preserve the cache's caller-owned slice")
	require.Zero(t, cache.snapshotCalls.Load())
}

type legacySnapshotReadCache struct {
	SchedulerCache
	accounts []*Account
	hit      bool
	err      error
}

func (c *legacySnapshotReadCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	return c.accounts, c.hit, c.err
}

func TestOutboxAuthoritativeSnapshotLegacyFallback(t *testing.T) {
	readErr := errors.New("snapshot unavailable")
	for _, tt := range []struct {
		name string
		hit  bool
		err  error
	}{
		{name: "hit", hit: true},
		{name: "miss"},
		{name: "error", err: readErr},
	} {
		t.Run(tt.name, func(t *testing.T) {
			original := &Account{ID: 1, Name: "original", Schedulable: false}
			legacy := &legacySnapshotReadCache{accounts: []*Account{nil, original}, hit: tt.hit, err: tt.err}
			cache := &outboxAuthoritativeSchedulerCache{SchedulerCache: legacy}
			accounts, hit, err := cache.GetSnapshotValues(context.Background(), SchedulerBucket{})
			require.ErrorIs(t, err, tt.err)
			require.Equal(t, tt.hit, hit)
			if !hit || err != nil {
				require.Nil(t, accounts)
				return
			}
			require.Len(t, accounts, 1, "the wrapper skips nil entries without filtering account state")
			require.Equal(t, *original, accounts[0])
			accounts[0].Name = "changed"
			require.Equal(t, "original", original.Name, "legacy pointer results must be copied")
		})
	}
}
