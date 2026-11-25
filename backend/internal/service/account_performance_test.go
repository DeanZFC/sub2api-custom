package service

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
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

func TestAccountPerformanceTrialReentersPoolAfterPausedCacheSnapshot(t *testing.T) {
	ctx := context.Background()
	due := time.Now().Add(-time.Second)
	r := &trialAdmissionStub{trialID: 1, dueAt: due}
	cached := &Account{ID: 1, Status: StatusActive, Schedulable: true, PerformancePaused: true}
	s := NewSchedulerSnapshotService(&performanceSnapshotCache{account: cached}, nil, r, nil, nil)
	got, err := s.GetAccount(ctx, 1)
	require.NoError(t, err)
	require.True(t, got.IsSchedulable())
	require.Equal(t, &due, got.PerformanceTrialDueAt)
	require.True(t, cached.PerformancePaused)
	// Ignore fields written by earlier snapshot formats. Only the routing read
	// for this request can supply a current performance pause or due time.
	var cachedCopy Account
	require.NoError(t, json.Unmarshal([]byte(`{"ID":1,"PerformancePaused":true}`), &cachedCopy))
	require.False(t, cachedCopy.PerformancePaused)
	wire, err := json.Marshal(got)
	require.NoError(t, err)
	require.NotContains(t, string(wire), "PerformanceTrialDueAt")
	require.NotContains(t, string(wire), "PerformancePaused")
}
func TestAccountPerformanceForwardAdmissionFailsClosed(t *testing.T) {
	repo := &performanceAccountRepoStub{paused: map[int64]bool{1: true}}
	gateway := &GatewayService{accountRepo: repo}
	_, vetoed, reason := gateway.AdmitAccountForForward(context.Background(), &Account{ID: 1})
	require.True(t, vetoed)
	require.Equal(t, "performance_paused", reason)
	repo.err = errors.New("authority unavailable")
	_, vetoed, reason = gateway.AdmitAccountForForward(context.Background(), &Account{ID: 1})
	require.True(t, vetoed)
	require.Equal(t, "performance_state_unavailable", reason)
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
