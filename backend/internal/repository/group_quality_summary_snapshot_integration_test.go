//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type groupQualitySummaryWriteAfterRead struct {
	groupQualitySummaryReader
	write func()
}

func (r *groupQualitySummaryWriteAfterRead) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	row := r.groupQualitySummaryReader.QueryRowContext(ctx, query, args...)
	if r.write != nil {
		write := r.write
		r.write = nil
		write()
	}
	return row
}

func TestGroupQualitySummaryConsistentSnapshotIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	now := time.Now()
	at := now.Add(-time.Minute)
	insertGroupTargetUsage(t, db, r, "first", "A", 8, at, 90, true)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback() }()

	reader := &groupQualitySummaryWriteAfterRead{groupQualitySummaryReader: tx, write: func() {
		// These timestamps precede the report cutoff, but their writes commit
		// after its first query has established the database snapshot.
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, Model: "A", RequestKey: "first", StartedAt: at, At: at}}, nil))
		insertGroupTargetUsage(t, db, r, "late", "A", 8, at, 50, true)
		_, err := db.ExecContext(ctx, `INSERT INTO group_quality_traffic_leases(lease_id,policy_id,policy_version,account_id,group_id,model,request_key,validation,fallback,reserved_input,created_at,fallback_expires_at)
 VALUES('late',$1,$2,62,8,'A','late',true,true,1000,$3,$4)`, p.ID, p.Version, at.Add(-time.Second), now.Add(time.Minute))
		require.NoError(t, err)
		_, err = db.ExecContext(ctx, `INSERT INTO group_quality_traffic_denials(policy_id,minute,requests) VALUES($1,$2,7)`, p.ID, at.Truncate(time.Minute))
		require.NoError(t, err)
		gap := at
		require.NoError(t, r.RecordFailures(ctx, nil, &gap))
	}}
	snapshot, err := readGroupQualityTargetSummary(ctx, reader, p, now)
	require.NoError(t, err)
	require.NoError(t, tx.Commit())
	require.Nil(t, reader.write, "the late writes must actually run between report queries")
	require.EqualValues(t, 1, snapshot.SuccessSamples)
	require.Zero(t, snapshot.FailureSamples)
	require.True(t, snapshot.TelemetryComplete)
	require.EqualValues(t, 100, snapshot.TotalInputTokens)
	require.Len(t, snapshot.Contributors, 1)
	require.Equal(t, snapshot.SuccessSamples, snapshot.Contributors[0].SuccessSamples)
	require.Equal(t, snapshot.FailureSamples, snapshot.Contributors[0].FailureSamples)
	require.Equal(t, snapshot.CacheInputTokens, snapshot.Contributors[0].CacheInputTokens)
	require.Equal(t, snapshot.CacheReadTokens, snapshot.Contributors[0].CacheReadTokens)
	require.Zero(t, snapshot.TrafficControl.ReservedInputTokens)
	require.Zero(t, snapshot.TrafficControl.LimitedRequests)
	require.Zero(t, snapshot.Fallback.Requests)
	require.Nil(t, snapshot.Fallback.LastUsedAt)

	later, err := r.groupQualityTargetSummary(ctx, p, now)
	require.NoError(t, err)
	require.EqualValues(t, 1, later.SuccessSamples)
	require.EqualValues(t, 1, later.FailureSamples)
	require.False(t, later.TelemetryComplete)
	require.EqualValues(t, 50, later.CacheReadTokens)
	require.EqualValues(t, 1000, later.TrafficControl.ReservedInputTokens)
	require.EqualValues(t, 7, later.TrafficControl.LimitedRequests)
	require.EqualValues(t, 1, later.Fallback.Requests)
	require.NotNil(t, later.Fallback.LastUsedAt)
	var successes, failures, input, read int64
	for _, contributor := range later.Contributors {
		successes += contributor.SuccessSamples
		failures += contributor.FailureSamples
		input += contributor.CacheInputTokens
		read += contributor.CacheReadTokens
	}
	require.Equal(t, later.SuccessSamples, successes)
	require.Equal(t, later.FailureSamples, failures)
	require.Equal(t, later.CacheInputTokens, input)
	require.Equal(t, later.CacheReadTokens, read)
}
