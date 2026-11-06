package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"math"
	"testing"
)

func TestAccountPerformanceBounds(t *testing.T) {
	n := func(v float64) *float64 { return &v }
	for _, tc := range []struct {
		name   string
		value  *float64
		bounds PerformanceBounds
		want   bool
	}{
		{"missing", nil, PerformanceBounds{Min: n(10)}, false},
		{"below", n(9), PerformanceBounds{Min: n(10)}, true},
		{"equal", n(10), PerformanceBounds{Min: n(10), Max: n(10)}, false},
		{"above", n(11), PerformanceBounds{Max: n(10)}, true},
	} {
		t.Run(tc.name, func(t *testing.T) { require.Equal(t, tc.want, tc.bounds.Breached(tc.value)) })
	}
	require.Error(t, (PerformanceBounds{Min: n(20), Max: n(10)}).Validate(false))
	require.Error(t, (PerformanceBounds{Max: n(101)}).Validate(true))
	require.Error(t, (PerformanceBounds{Min: n(math.NaN())}).Validate(false))
}
func TestAccountPerformancePauseIsIndependent(t *testing.T) {
	a := &Account{Status: StatusActive, Schedulable: true, PerformancePaused: true}
	require.False(t, a.IsSchedulable())
	a.Schedulable = false
	a.PerformancePaused = false
	require.False(t, a.IsSchedulable())
	a.Schedulable = true
	require.True(t, a.IsSchedulable())
}

type performanceAccountRepoStub struct {
	AccountRepository
	paused map[int64]bool
	err    error
	calls  int
}

func (r *performanceAccountRepoStub) PerformancePaused(context.Context, []int64) (map[int64]bool, error) {
	r.calls++
	return r.paused, r.err
}
func TestAccountPerformanceSnapshotRejectsStaleCacheAndFailsClosed(t *testing.T) {
	ctx := context.Background()
	cached := &Account{ID: 1, Status: StatusActive, Schedulable: true}
	repo := &performanceAccountRepoStub{paused: map[int64]bool{1: true}}
	cache := &performanceSnapshotCache{account: cached}
	s := NewSchedulerSnapshotService(cache, nil, repo, nil, nil)
	rows, _, err := s.ListSchedulableAccounts(ctx, nil, PlatformOpenAI, false)
	require.NoError(t, err)
	require.Empty(t, rows)
	a, err := s.GetAccount(ctx, 1)
	require.NoError(t, err)
	require.False(t, a.IsSchedulable())
	batch, err := s.GetAccounts(ctx, []int64{1})
	require.NoError(t, err)
	require.False(t, batch[1].IsSchedulable())
	require.False(t, cached.PerformancePaused, "never mutate shared cache pointers")
	repo.err = errors.New("authority unavailable")
	rows, _, err = s.ListSchedulableAccounts(ctx, nil, PlatformOpenAI, false)
	require.Error(t, err)
	require.Nil(t, rows)
	a, err = s.GetAccount(ctx, 1)
	require.Error(t, err)
	require.Nil(t, a)
	batch, err = s.GetAccounts(ctx, []int64{1})
	require.Error(t, err)
	require.Nil(t, batch)
}
func TestAccountPerformanceSlotRejectsPauseBeforeUnlimitedOrCache(t *testing.T) {
	repo := &performanceAccountRepoStub{paused: map[int64]bool{1: true}}
	svc := NewConcurrencyService(nil)
	svc.performancePauses = repo
	for _, limit := range []int{0, 5} {
		r, err := svc.AcquireAccountSlot(context.Background(), 1, limit)
		require.NoError(t, err)
		require.False(t, r.Acquired)
	}
	repo.err = errors.New("authority unavailable")
	r, err := svc.AcquireAccountSlot(context.Background(), 1, 0)
	require.Error(t, err)
	require.Nil(t, r)
}

type performanceSnapshotCache struct {
	SchedulerCache
	account *Account
}

func (c *performanceSnapshotCache) GetSnapshot(context.Context, SchedulerBucket) ([]*Account, bool, error) {
	return []*Account{c.account}, true, nil
}
func (c *performanceSnapshotCache) GetAccount(context.Context, int64) (*Account, error) {
	return c.account, nil
}
func (c *performanceSnapshotCache) GetAccounts(context.Context, []int64) (map[int64]*Account, error) {
	return map[int64]*Account{c.account.ID: c.account}, nil
}
