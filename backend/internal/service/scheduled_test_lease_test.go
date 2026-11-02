package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type heartbeatLeaseRepo struct {
	alive    bool
	err      error
	releases atomic.Int32
}

func (r *heartbeatLeaseRepo) ClaimExecution(context.Context, *ScheduledTestPlan, string, string, bool) (bool, error) {
	return true, nil
}
func (r *heartbeatLeaseRepo) RenewExecution(context.Context, int64, string, string) (bool, error) {
	return r.alive, r.err
}
func (r *heartbeatLeaseRepo) ReleaseExecution(context.Context, int64, string, string) error {
	r.releases.Add(1)
	return nil
}
func TestScheduledTestLeaseHeartbeatCancelsOnOwnershipLoss(t *testing.T) {
	for _, cause := range []error{nil, errors.New("database unavailable")} {
		repo := &heartbeatLeaseRepo{err: cause}
		ctx, release, err := keepScheduledTestLease(context.Background(), repo, 1, "plan", "owner", time.Millisecond)
		require.NoError(t, err)
		select {
		case <-ctx.Done():
		case <-time.After(time.Second):
			t.Fatal("lost lease did not cancel work")
		}
		release()
		release()
		require.EqualValues(t, 1, repo.releases.Load())
	}
}
func TestScheduledTestLeaseStopsHeartbeatBeforeRelease(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	repo := &heartbeatLeaseRepo{alive: true}
	ctx, release, err := keepScheduledTestLease(parent, repo, 1, "plan", "owner", time.Hour)
	require.NoError(t, err)
	cancel()
	release()
	require.ErrorIs(t, ctx.Err(), context.Canceled)
	require.EqualValues(t, 1, repo.releases.Load())
}
