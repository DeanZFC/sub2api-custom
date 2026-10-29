//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func newGroupTargetPolicy(t *testing.T, ctx context.Context, db *sql.DB, r *passiveQualityRepository) service.PassiveQualityPolicy {
	t.Helper()
	p := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Purpose: "group_quality_target", RecoveryMode: "active", Name: "group target", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 80, Recover: 85}}}
	require.NoError(t, r.SavePolicy(ctx, &p))
	p.UpdatedAt = time.Now().Add(-time.Minute)
	_, err := db.Exec(`UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1`, p.ID, p.UpdatedAt)
	require.NoError(t, err)
	return p
}

func insertGroupTargetUsage(t *testing.T, db *sql.DB, r *passiveQualityRepository, key, model string, group int64, at time.Time, read int64, reported bool) {
	t.Helper()
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,group_id,request_id,created_at,model,requested_model,input_tokens,cache_read_tokens,stream,request_type,first_token_ms) VALUES(62,$1,$2,$3,'provider',$4,$5,$6,false,1,50)`, group, key, at, model, 100-read, read)
	require.NoError(t, err)
	if reported {
		require.NoError(t, r.RecordFailures(context.Background(), []service.PassiveQualityFailure{{AccountID: 62, RequestKey: key, At: at, CacheReported: true}}, nil))
	}
}

func targetStates(t *testing.T, ctx context.Context, r *passiveQualityRepository) map[string]service.PassiveQualityState {
	t.Helper()
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	out := map[string]service.PassiveQualityState{}
	for _, s := range states {
		if s.Purpose == "group_quality_target" {
			out[s.Model] = s
		}
	}
	return out
}

func TestGroupQualityTargetMeasuredCacheLifecycleIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	base := time.Now().Add(-10 * time.Minute)
	_, err := db.Exec(`UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1`, p.ID, base.Add(-time.Second))
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("bad-%d", i), "A", 8, at, 0, true)
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("healthy-%d", i), "B", 8, at, 90, true)
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("unknown-%d", i), "unknown", 8, at, 0, false)
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("other-%d", i), "other-group", 9, at, 0, true)
	}
	_, err = r.Sweep(ctx, base.Add(time.Second), 0)
	require.NoError(t, err)
	states := targetStates(t, ctx, r)
	require.Len(t, states, 3)
	require.Equal(t, "suspect", states["A"].Phase)
	require.Equal(t, 20, states["A"].Evidence.CacheSamples)
	require.Equal(t, "healthy", states["B"].Phase)
	require.Equal(t, "insufficient", states["unknown"].Phase)
	// Sustain a full fresh batch each minute for the configured five-minute gate.
	for batch := 1; batch <= 6; batch++ {
		for i := 0; i < 5; i++ {
			insertGroupTargetUsage(t, db, r, fmt.Sprintf("bad-next-%d-%d", batch, i), "A", 8, base.Add(time.Duration(batch)*time.Minute+time.Duration(i)*time.Millisecond), 0, true)
		}
		_, err = r.advance(ctx, p.ID, 62, base.Add(time.Duration(batch)*time.Minute+time.Second), false, "A")
		require.NoError(t, err)
	}
	require.Equal(t, "paused", targetStates(t, ctx, r)["A"].Phase)
	allowed, reserved, err := r.AcquirePassiveQuality(ctx, 62, "A", "existing-owner")
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, reserved)
	allowed, _, err = r.AcquirePassiveQuality(ctx, 62, "B", "healthy-other-model")
	require.NoError(t, err)
	require.True(t, allowed)
	// Recorded successes cannot emulate an active recovery result.
	for i := 0; i < 20; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("late-success-%d", i), "A", 8, base.Add(7*time.Minute+time.Duration(i)*time.Millisecond), 99, true)
	}
	_, err = r.advance(ctx, p.ID, 62, base.Add(8*time.Minute), false, "A")
	require.NoError(t, err)
	require.Equal(t, "paused", targetStates(t, ctx, r)["A"].Phase)
	var status string
	var schedulable bool
	require.NoError(t, db.QueryRow(`SELECT status,schedulable FROM accounts WHERE id=62`).Scan(&status, &schedulable))
	require.Equal(t, "active", status)
	require.True(t, schedulable)
}

func TestGroupQualityTargetFailureMembershipAndReplayIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.CacheRate.Enabled = false
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 15}
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 10000, Recover: 8000}
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := time.Now().Add(-20 * time.Second)
	_, err := db.Exec(`UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1`, p.ID, base.Add(-time.Second))
	require.NoError(t, err)
	for i := 0; i < 20; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, Model: "bad", RequestKey: fmt.Sprintf("bad-%d", i), StartedAt: at, At: at}, {AccountID: 62, GroupID: 9, Model: "other-failure", RequestKey: fmt.Sprintf("other-fail-%d", i), StartedAt: at, At: at}}, nil))
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("slow-%d", i), "slow", 8, at, 90, true)
	}
	_, err = db.Exec(`UPDATE usage_logs SET stream=true,request_type=2,first_token_ms=10001 WHERE requested_model='slow'`)
	require.NoError(t, err)
	_, err = r.Sweep(ctx, base.Add(time.Second), 0)
	require.NoError(t, err)
	states := targetStates(t, ctx, r)
	require.Len(t, states, 2)
	require.Equal(t, "paused", states["bad"].Phase)
	require.Equal(t, 100.0, *states["bad"].Evidence.ErrorRate)
	require.Equal(t, "suspect", states["slow"].Phase)
	require.Contains(t, states["slow"].Reason, "ttft_p90")
	// A missing group on a new failed attempt means incomplete availability, not healthy.
	unknownAt := base.Add(2 * time.Second)
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "slow", RequestKey: "unknown-fail", StartedAt: unknownAt, At: unknownAt}}, nil))
	_, err = r.advance(ctx, p.ID, 62, base.Add(3*time.Second), false, "slow")
	require.NoError(t, err)
	require.False(t, targetStates(t, ctx, r)["slow"].Evidence.Complete)
	require.Equal(t, "insufficient", targetStates(t, ctx, r)["slow"].Phase)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	models, err := passiveReplayModels(ctx, tx, p, 62, base.Add(-time.Second), base.Add(4*time.Second))
	require.NoError(t, err)
	require.ElementsMatch(t, []string{"bad", "slow"}, models)
	p.Model = "bad"
	s := service.PassiveQualityState{AccountID: 62, Model: "bad", Phase: "healthy"}
	point, _, missing, err := replayPassiveMetrics(ctx, tx, p, &s, base.Add(time.Second), time.Now())
	require.NoError(t, err)
	require.Equal(t, "fail", point.Verdict)
	require.False(t, missing)
	require.Nil(t, point.Cache)
}

func TestGroupQualityTargetExcludesGeneratedMediaIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 15}
	base := time.Now().Add(-10 * time.Second)
	insertGroupTargetUsage(t, db, r, "text", "text", 8, base, 90, true)
	insertGroupTargetUsage(t, db, r, "vision-text", "text", 8, base, 90, true)
	_, err := db.Exec(`UPDATE usage_logs SET input_tokens=30,image_input_tokens=20 WHERE request_id='vision-text'`)
	require.NoError(t, err)
	for _, media := range []struct{ key, change string }{
		{"image-count", "image_count=1"},
		{"image-output", "image_output_tokens=100"},
		{"video-count", "video_count=1"},
		{"media-inbound", "inbound_endpoint='/v1/audio/speech'"},
		{"media-upstream", "upstream_endpoint='/v1/images/generations'"},
	} {
		insertGroupTargetUsage(t, db, r, media.key, media.key, 8, base, 0, true)
		_, err = db.Exec(`UPDATE usage_logs SET `+media.change+` WHERE request_id=$1`, media.key)
		require.NoError(t, err)
	}
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{
		{AccountID: 62, GroupID: 8, Model: "text", RequestKey: "text-fail", StartedAt: base, At: base},
		{AccountID: 62, GroupID: 8, Model: "media-fail", RequestKey: "media-fail", StartedAt: base, At: base, NonText: true},
		{AccountID: 62, Model: "text", RequestKey: "unknown-media-fail", StartedAt: base, At: base, NonText: true},
	}, nil))
	targets, err := r.modelTargets(ctx, p, base.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []passiveModelTarget{{account: 62, model: "text"}}, targets)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	models, err := passiveReplayModels(ctx, tx, p, 62, base.Add(-time.Second), base.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, []string{"text"}, models)
	p.Model = "text"
	evidence, err := passiveGroupTargetSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62}, base.Add(time.Second))
	require.NoError(t, err)
	require.True(t, evidence.Complete, "unattributed media failures must not invalidate text availability")
	require.Equal(t, 3, evidence.Samples)
	require.Equal(t, 1, evidence.Failures)
	require.Equal(t, 2, evidence.CacheSamples)
	require.Equal(t, 90.0, *evidence.CacheRate, "image input tokens must be removed from text-cache denominator")
}
