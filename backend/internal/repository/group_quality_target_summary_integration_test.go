//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityTargetLiveSummaryIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{
		Purpose: "group_quality_target", GroupIDs: []int64{8}, Enabled: true, Mode: "enforce",
		CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80},
	}}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	insert := func(key string, group, input, read int, stream, reported bool, ttft any) {
		t.Helper()
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,group_id,created_at,input_tokens,cache_read_tokens,stream,first_token_ms) VALUES(62,$1,$2,$3,$4,$5,$6,$7)`, key, group, now.Add(-time.Minute), input, read, stream, ttft)
		require.NoError(t, err)
		if reported {
			require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, RequestKey: key, CacheReported: true, At: now.Add(-time.Minute)}}, nil))
		}
	}
	insert("cached", 8, 100, 900, true, true, 5000)
	insert("explicit-zero-nonstream", 8, 100, 0, false, true, 99999)
	insert("unknown", 8, 100, 0, true, false, 9000)
	insert("another-group", 9, 100000, 0, true, true, 99000)
	insert("media-output", 8, 100000, 0, true, true, 99000)
	_, err := db.Exec(`UPDATE usage_logs SET image_count=1 WHERE request_id='media-output'`)
	require.NoError(t, err)
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, Model: "image-model", RequestKey: "media-failure", NonText: true, StartedAt: now.Add(-2 * time.Minute), At: now.Add(-time.Minute)}}, nil))
	insert("partial-failed", 8, 100, 900, true, true, 1000)
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, Model: "model", RequestKey: "partial-failed", StartedAt: now.Add(-2 * time.Minute), At: now.Add(-time.Minute)}}, nil))
	read := func() service.GroupQualityTargetSummary {
		t.Helper()
		computed, err := r.groupQualityTargetSummary(ctx, p, now)
		require.NoError(t, err)
		require.NoError(t, r.publishGroupQualitySummary(ctx, p, computed))
		out, err := r.GroupQualityTargetSummaries(ctx, now)
		require.NoError(t, err)
		require.Len(t, out, 1)
		return out[0]
	}
	s := read()
	require.Equal(t, p.ID, s.PolicyID)
	require.EqualValues(t, 8, s.GroupID)
	require.True(t, s.TelemetryComplete)
	require.EqualValues(t, 3, s.SuccessSamples)
	require.EqualValues(t, 1, s.FailureSamples)
	require.NotNil(t, s.ActualAvailability)
	require.InDelta(t, 75, *s.ActualAvailability, .00001)
	require.EqualValues(t, 2, s.CacheSamples)
	require.EqualValues(t, 1, s.CacheUnknownSamples)
	require.EqualValues(t, 900, s.CacheReadTokens)
	require.EqualValues(t, 1100, s.CacheInputTokens)
	require.NotNil(t, s.ActualCacheRate)
	require.InDelta(t, 900.0/1100*100, *s.ActualCacheRate, .00001)
	require.EqualValues(t, 2, s.TTFTSamples)
	require.NotNil(t, s.TTFTP90MS)
	require.Equal(t, 9000.0, *s.TTFTP90MS)
	// Historical failures without group attribution cannot establish success.
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: "legacy-unknown", StartedAt: now.Add(-2 * time.Minute), At: now.Add(-time.Minute)}}, nil))
	s = read()
	require.EqualValues(t, 1, s.FailureUnknownSamples)
	require.Nil(t, s.ActualAvailability)
	require.NotNil(t, s.ActualCacheRate)
	// A dropped telemetry batch makes every displayed rate unknown.
	gap := now.Add(-time.Second)
	require.NoError(t, r.RecordFailures(ctx, nil, &gap))
	s = read()
	require.False(t, s.TelemetryComplete)
	require.Nil(t, s.ActualCacheRate)
	require.Nil(t, s.ActualAvailability)
	require.Nil(t, s.TTFTP90MS)
	// A paused state's old evidence must not leak into the current window.
	_, err = db.Exec(`INSERT INTO passive_quality_states(policy_id,account_id,model_key,data) VALUES($1,62,'model',$2::jsonb)`, p.ID, `{"phase":"paused","evidence":{"cache_rate":99,"ttft_p90":1,"samples":100}}`)
	require.NoError(t, err)
	require.NoError(t, r.refreshGroupQualityTrafficSnapshot(ctx, p, now.Add(2*time.Hour)))
	newer, err := r.GroupQualityTargetSummaries(ctx, now.Add(2*time.Hour))
	require.NoError(t, err)
	require.Len(t, newer, 1)
	require.Nil(t, newer[0].ActualCacheRate)
	require.Nil(t, newer[0].ActualAvailability)
	require.Nil(t, newer[0].TTFTP90MS)
	require.Zero(t, newer[0].SuccessSamples)
}

func TestGroupQualityTargetSummaryScopeIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Purpose: "group_quality_target", GroupIDs: []int64{8}, Model: "target", ExcludedAccountIDs: []int64{63}, Enabled: true, Mode: "enforce", CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80}}}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,group_id,request_id,model,created_at,input_tokens,cache_read_tokens,first_token_ms) VALUES(62,8,'kept','target',$1,10,90,5000),(62,8,'other-model','other',$1,999999,1,99999),(63,8,'excluded','target',$1,999999,1,99999)`, now.Add(-time.Minute))
	require.NoError(t, err)
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 63, GroupID: 8, Model: "target", RequestKey: "excluded-failure", StartedAt: now.Add(-2 * time.Minute), At: now.Add(-time.Minute)}, {AccountID: 62, GroupID: 8, Model: "other", RequestKey: "other-model-failure", StartedAt: now.Add(-2 * time.Minute), At: now.Add(-time.Minute)}}, nil))
	computed, err := r.groupQualityTargetSummary(ctx, p, now)
	require.NoError(t, err)
	require.NoError(t, r.publishGroupQualitySummary(ctx, p, computed))
	out, err := r.GroupQualityTargetSummaries(ctx, now)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.EqualValues(t, 1, out[0].SuccessSamples)
	require.Zero(t, out[0].FailureSamples)
	require.NotNil(t, out[0].ActualCacheRate)
	require.InDelta(t, 90, *out[0].ActualCacheRate, .00001)
	require.NotNil(t, out[0].ActualAvailability)
	require.Equal(t, 100.0, *out[0].ActualAvailability)
}
