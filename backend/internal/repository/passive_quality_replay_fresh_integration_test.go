//go:build integration

package repository

import (
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

// Fresh request-side evidence must produce the same cache lifecycle through
// live sweeps and historical evaluation, and later points cannot rewrite older
// evidence snapshots retained for the administrator.
func TestPassiveQualityFreshMetadataLiveReplayParityIntegration(t *testing.T) {
	ctx, db, repo := passiveDB(t)
	p := v2CachePolicy()
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 100, Recover: 80}
	require.NoError(t, repo.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	seed := func(name string, offset, count int, read int64) {
		t.Helper()
		for i := 0; i < count; i++ {
			recordCacheRequest(t, ctx, db, repo, 62, fmt.Sprintf("%s-%d", name, i), fmt.Sprintf("session-%d", i%2), "prefix", base.Add(time.Duration(offset+i)*time.Second), read)
		}
	}
	seed("bad", 0, 8, 0)
	seed("bad-new", 11, 2, 0)
	seed("recover1", 44, 6, 1024)
	seed("recover1-new", 52, 2, 1024)
	seed("recover2", 86, 6, 1024)
	seed("recover2-new", 94, 2, 1024)
	seed("recover3", 128, 6, 1024)
	seed("recover3-new", 136, 2, 1024)
	replay := service.PassiveQualityState{AccountID: 62, Phase: "insufficient", ObserveAfter: p.UpdatedAt}
	var snapshots []service.QualityReplayPoint
	var serialized [][]byte
	for _, offset := range []int{9, 10, 13, 40, 51, 55, 93, 97, 135, 139} {
		at := base.Add(time.Duration(offset) * time.Second)
		_, err := repo.advance(ctx, p.ID, 62, at, false)
		require.NoError(t, err)
		states, err := repo.ListStates(ctx, 62)
		require.NoError(t, err)
		require.Len(t, states, 1)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		point, _, missing, err := replayPassiveMetrics(ctx, tx, p, &replay, at, base.Add(3*time.Minute))
		require.NoError(t, err)
		require.NoError(t, tx.Rollback())
		require.False(t, missing)
		// Live state has passed through JSONB persistence. Compare the public
		// representation so omitted empty maps and nil maps remain equivalent.
		liveCache, err := json.Marshal(states[0].Cache)
		require.NoError(t, err)
		replayCache, err := json.Marshal(point.Cache)
		require.NoError(t, err)
		require.JSONEq(t, string(liveCache), string(replayCache), "cache mismatch at +%ds", offset)
		liveLatency, err := json.Marshal(states[0].Latency)
		require.NoError(t, err)
		replayLatency, err := json.Marshal(point.Latency)
		require.NoError(t, err)
		require.JSONEq(t, string(liveLatency), string(replayLatency), "TTFT mismatch at +%ds", offset)
		snapshots = append(snapshots, point)
		raw, err := json.Marshal(point)
		require.NoError(t, err)
		serialized = append(serialized, raw)
	}
	require.Equal(t, "reduced", snapshots[2].Cache["model"].Phase)
	require.Equal(t, 25, snapshots[2].Cache["model"].AllocationPercent)
	require.Equal(t, 50, snapshots[5].Cache["model"].AllocationPercent)
	require.Equal(t, 75, snapshots[7].Cache["model"].AllocationPercent)
	require.Equal(t, "healthy", snapshots[9].Cache["model"].Phase)
	require.Equal(t, 100, snapshots[9].Cache["model"].AllocationPercent)
	for i, point := range snapshots {
		raw, err := json.Marshal(point)
		require.NoError(t, err)
		require.JSONEq(t, string(serialized[i]), string(raw), "later evaluations must not mutate prior replay point %d", i)
	}
}
