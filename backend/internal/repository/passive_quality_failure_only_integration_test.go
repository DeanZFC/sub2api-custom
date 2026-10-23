//go:build integration

package repository

import (
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityTargetAllFailuresPauseWithoutUsageIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 15}
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 15000, Recover: 12000}
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := time.Now().Add(-20 * time.Second)
	_, err := db.Exec(`UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1`, p.ID, base.Add(-time.Second))
	require.NoError(t, err)
	insertFailures := func(start, end int, at time.Time) {
		t.Helper()
		for i := start; i < end; i++ {
			timestamp := at.Add(time.Duration(i) * time.Millisecond)
			failure := service.PassiveQualityFailure{AccountID: 62, GroupID: 8, Model: "failed-model", RequestKey: fmt.Sprintf("failed-%d", i), StartedAt: timestamp, At: timestamp}
			require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{failure}, nil))
		}
	}
	insertFailures(0, 5, base)
	_, err = r.Sweep(ctx, base.Add(time.Second), 0)
	require.NoError(t, err)
	state := targetStates(t, ctx, r)["failed-model"]
	require.Equal(t, "suspect", state.Phase)
	require.Equal(t, 1, state.Hits)
	require.True(t, state.Evidence.Complete)
	require.Equal(t, 100.0, *state.Evidence.ErrorRate)
	require.Nil(t, state.Evidence.TTFT)
	require.Nil(t, state.Evidence.CacheRate)
	// Re-reading the same failures must not consume another confirmation.
	_, err = r.Sweep(ctx, base.Add(2*time.Second), 0)
	require.NoError(t, err)
	require.Equal(t, "suspect", targetStates(t, ctx, r)["failed-model"].Phase)
	insertFailures(5, 7, base.Add(3*time.Second))
	_, err = r.Sweep(ctx, base.Add(4*time.Second), 0)
	require.NoError(t, err)
	state = targetStates(t, ctx, r)["failed-model"]
	require.Equal(t, "paused", state.Phase)
	require.Contains(t, state.Reason, "error_rate")
	require.Equal(t, 7, state.Evidence.Samples)
	allowed, _, err := r.AcquirePassiveQuality(ctx, 62, "failed-model", "after-failure-confirmation")
	require.NoError(t, err)
	require.False(t, allowed, "availability failures must block routing without waiting for cache or TTFT samples")
}
