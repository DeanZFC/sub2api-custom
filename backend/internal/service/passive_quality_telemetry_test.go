package service

import (
	"context"
	"errors"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type passiveTelemetryTestRepo struct {
	PassiveQualityRepository
	record  func(context.Context, []PassiveQualityFailure, []PassiveQualityTelemetryGap) error
	cleanup func(context.Context) error
}

func (r *passiveTelemetryTestRepo) RecordTelemetry(ctx context.Context, f []PassiveQualityFailure, g []PassiveQualityTelemetryGap) error {
	return r.record(ctx, f, g)
}
func (r *passiveTelemetryTestRepo) PrunePassiveQuality(ctx context.Context) error {
	if r.cleanup != nil {
		return r.cleanup(ctx)
	}
	return nil
}

func TestPassiveQualityTelemetryRetryRetainsScopedLossAndRecords(t *testing.T) {
	repo := &passiveTelemetryTestRepo{}
	svc := NewPassiveQualityService(repo, nil)
	defer svc.cancel()
	svc.queue = make(chan PassiveQualityFailure, 1)
	svc.enabled.Store(true)
	at := time.Now().Add(-time.Minute)
	svc.enqueue(PassiveQualityFailure{AccountID: 62, GroupID: 8, Model: "model", RequestKey: "kept", At: at})
	svc.enqueue(PassiveQualityFailure{AccountID: 63, GroupID: 9, Model: "other", RequestKey: "dropped", At: at, CacheReported: true})
	calls := 0
	repo.record = func(ctx context.Context, f []PassiveQualityFailure, g []PassiveQualityTelemetryGap) error {
		calls++
		require.Len(t, f, 1)
		require.Equal(t, "kept", f[0].RequestKey)
		byAccount := map[int64]PassiveQualityTelemetryGap{}
		for _, gap := range g {
			byAccount[gap.AccountID] = gap
		}
		if calls == 1 {
			require.Len(t, g, 1)
		} else {
			require.Len(t, g, 2)
			require.EqualValues(t, 8, byAccount[62].GroupID)
			require.Equal(t, "model", byAccount[62].Model)
			require.Equal(t, "failure", byAccount[62].Kind)
			require.True(t, byAccount[62].To.After(byAccount[62].From))
		}
		require.EqualValues(t, 9, byAccount[63].GroupID)
		require.Equal(t, "other", byAccount[63].Model)
		require.Equal(t, "cache", byAccount[63].Kind)
		require.EqualValues(t, 1, byAccount[63].DroppedRecords)
		deadline, ok := ctx.Deadline()
		require.True(t, ok)
		require.LessOrEqual(t, time.Until(deadline), 3*time.Second)
		if calls == 1 {
			return errors.New("database unavailable")
		}
		return nil
	}
	require.False(t, svc.flush(true))
	d := svc.Diagnostics()
	require.EqualValues(t, 1, d.PendingRecords)
	require.EqualValues(t, 1, d.DroppedRecords)
	require.Equal(t, 2, d.PendingGapScopes)
	require.Contains(t, d.LastPersistError, "database unavailable")
	require.True(t, svc.flush(true))
	d = svc.Diagnostics()
	require.Zero(t, d.PendingRecords)
	require.Zero(t, d.PendingGapScopes)
	require.Empty(t, d.LastPersistError)
	require.NotNil(t, d.LastPersistedAt)
	require.Zero(t, d.CollectionLagSeconds)
}

func TestPassiveQualityTelemetryBatchUsesSingleBudget(t *testing.T) {
	repo := &passiveTelemetryTestRepo{}
	svc := NewPassiveQualityService(repo, nil)
	defer svc.cancel()
	svc.enabled.Store(true)
	for i := 0; i < 1200; i++ {
		svc.enqueue(PassiveQualityFailure{AccountID: 62, RequestKey: "batch", At: time.Now()})
	}
	var deadline time.Time
	sizes := []int{}
	repo.record = func(ctx context.Context, f []PassiveQualityFailure, g []PassiveQualityTelemetryGap) error {
		d, ok := ctx.Deadline()
		require.True(t, ok)
		if deadline.IsZero() {
			deadline = d
		} else {
			require.Equal(t, deadline, d)
		}
		sizes = append(sizes, len(f))
		return nil
	}
	require.True(t, svc.flush(true))
	require.Equal(t, []int{512, 512, 176}, sizes)
}

func TestPassiveQualityTelemetryScopeMemoryIsBounded(t *testing.T) {
	svc := NewPassiveQualityService(nil, nil)
	defer svc.cancel()
	for i := int64(1); i <= 300; i++ {
		svc.markGap(PassiveQualityTelemetryGap{From: time.Now(), AccountID: i, Kind: "cache", DroppedRecords: 1})
	}
	require.LessOrEqual(t, svc.Diagnostics().PendingGapScopes, 257)
	var total int64
	global := false
	for _, g := range svc.takeGaps() {
		total += g.DroppedRecords
		if g.AccountID == 0 && g.Kind == "all" {
			global = true
		}
	}
	require.EqualValues(t, 300, total)
	require.True(t, global)
}

func TestPassiveQualityCleanupRunsWithoutEvaluator(t *testing.T) {
	called := make(chan struct{}, 1)
	repo := &passiveTelemetryTestRepo{cleanup: func(ctx context.Context) error { called <- struct{}{}; return nil }}
	svc := NewPassiveQualityService(repo, nil)
	svc.wg.Add(1)
	go svc.maintenanceLoop()
	select {
	case <-called:
	case <-time.After(time.Second):
		t.Fatal("cleanup was coupled to evaluation")
	}
	svc.Stop()
	require.NotNil(t, svc.Diagnostics().LastCleanupSucceededAt)
}
