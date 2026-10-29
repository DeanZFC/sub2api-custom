//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
	"os"
	"testing"
	"time"
)

func activeSamplesForTest(t *testing.T, ctx context.Context, db *sql.DB, p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) service.PassiveQualityEvidence {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	e, err := passiveActiveGroupTargetSamples(ctx, tx, p, s, now)
	require.NoError(t, err)
	return e
}
func TestActiveTargetWholeHourAggregatesBeyondHundredIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := activeTargetPolicy(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	base := now.Add(-10 * time.Minute)
	_, err := db.Exec("INSERT INTO usage_logs(account_id,group_id,request_id,created_at,model,requested_model,input_tokens,cache_read_tokens,output_tokens,first_token_ms,duration_ms) SELECT 62,8,'full-'||n,$1::timestamptz+n*INTERVAL '1 second','upstream','A',CASE WHEN n<=100 THEN 1000 ELSE 10 END,CASE WHEN n<=100 THEN 0 ELSE 90 END,CASE WHEN n<=100 THEN 100 ELSE 200 END,CASE WHEN n<=100 THEN 1000 ELSE 100 END,CASE WHEN n<=100 THEN 2000 ELSE 2100 END FROM generate_series(1,200)n", base)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO passive_quality_cache_reports(account_id,request_key,cache_reported,created_at) SELECT 62,'full-'||n,true,$1::timestamptz+n*INTERVAL '1 second' FROM generate_series(1,200)n", base)
	require.NoError(t, err)
	for _, x := range []struct {
		key, model string
		group      int64
		at         time.Time
	}{{"foreign-group", "A", 9, now}, {"foreign-model", "B", 8, now}, {"old-hour", "A", 8, now.Add(-61 * time.Minute)}} {
		insertGroupTargetUsage(t, db, r, x.key, x.model, x.group, x.at, 0, true)
	}
	s := service.PassiveQualityState{AccountID: 62, Model: "A", ObserveAfter: now.Add(-2 * time.Hour), LastEvidenceAt: base.Add(190 * time.Second), RecoveredAt: base.Add(150 * time.Second)}
	e := activeSamplesForTest(t, ctx, db, p, s, now)
	require.Equal(t, 200, e.Samples)
	require.Equal(t, 200, e.CacheSamples)
	require.EqualValues(t, 110000, e.CacheInputTokens)
	require.EqualValues(t, 9000, e.CacheReadTokens)
	require.InDelta(t, 9000.0/110000*100, *e.CacheRate, 0.00001)
	require.Equal(t, 1000.0, *e.TTFT, "P90 includes early slow requests, not just the newest hundred")
	require.Equal(t, 100.0, *e.OutputTPS)
	require.Equal(t, 10, e.Fresh)
	require.NotNil(t, e.SinceRecovery)
	require.Equal(t, 48, e.SinceRecovery.Samples, "requests starting before recovery cannot establish a new episode")
	require.Equal(t, 90.0, *e.SinceRecovery.CacheRate)
	require.True(t, e.Complete)
}
func TestActiveTargetIndependentMetricFreshnessIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := activeTargetPolicy(t)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 5, Recover: 1}
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := service.PassiveQualityState{AccountID: 62, Model: "A", ObserveAfter: now.Add(-time.Hour), Phase: "healthy"}
	for i := 0; i < 20; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("initial-%d", i), "A", 8, now.Add(-time.Minute+time.Duration(i)*time.Millisecond), 0, true)
	}
	e := activeSamplesForTest(t, ctx, db, p, s, now)
	advancePassiveObserved(p, &s, e, now)
	cacheCursor := s.ActiveConfirmations["cache_rate"].Latest
	for i := 0; i < 5; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("partial-%d", i), "A", 8, now.Add(time.Duration(i+1)*time.Second), 0, i < 3)
	}
	for i := 0; i < 2; i++ {
		at := now.Add(time.Duration(i+6) * time.Second)
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, Model: "A", RequestKey: fmt.Sprintf("failed-partial-%d", i), At: at, StartedAt: at}}, nil))
	}
	e = activeSamplesForTest(t, ctx, db, p, s, now.Add(10*time.Second))
	require.Equal(t, 3, e.CacheFresh)
	advancePassiveObserved(p, &s, e, now.Add(10*time.Second))
	require.Equal(t, cacheCursor, s.ActiveConfirmations["cache_rate"].Latest)
	require.True(t, s.LastEvidenceAt.After(cacheCursor), "availability consumes its complete new batch while cache retains three samples")
	for i := 0; i < 2; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("complete-%d", i), "A", 8, now.Add(time.Duration(i+11)*time.Second), 0, true)
	}
	e = activeSamplesForTest(t, ctx, db, p, s, now.Add(15*time.Second))
	require.Equal(t, 5, e.CacheFresh, "three earlier cache observations survive other metric updates")
	advancePassiveObserved(p, &s, e, now.Add(15*time.Second))
	require.Equal(t, 2, s.ActiveConfirmations["cache_rate"].Hits)
}
func TestActiveTargetFailuresOverrideAttemptsAndRapidCircuitIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := activeTargetPolicy(t)
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 5}
	now := time.Now().UTC().Truncate(time.Microsecond)
	for i := 0; i < 150; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("ok-%d", i), "A", 8, now.Add(-time.Minute+time.Duration(i)*time.Millisecond), 90, true)
	}
	for i := 0; i < 3; i++ {
		at := now.Add(-time.Duration(3-i) * time.Second)
		key := fmt.Sprintf("failed-%d", i)
		insertGroupTargetUsage(t, db, r, key, "A", 8, at, 90, true)
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, GroupID: 8, RequestKey: key, Model: "A", At: at, StartedAt: at}}, nil))
	}
	s := service.PassiveQualityState{AccountID: 62, Model: "A", ObserveAfter: now.Add(-time.Hour)}
	e := activeSamplesForTest(t, ctx, db, p, s, now)
	require.Equal(t, 153, e.Samples)
	require.Equal(t, 3, e.Failures)
	require.Equal(t, 3, e.RecentConsecutiveFailures)
	require.Equal(t, 150, e.CacheSamples)
	advancePassiveObserved(p, &s, e, now)
	require.Equal(t, "paused", s.Phase, "three fresh upstream failures are not diluted by historical successes")
}
func newActiveTargetPolicy(t *testing.T, ctx context.Context, db *sql.DB, r *passiveQualityRepository, now time.Time) service.PassiveQualityPolicy {
	t.Helper()
	p := activeTargetPolicy(t)
	require.NoError(t, r.SavePolicy(ctx, &p))
	p.UpdatedAt = now.Add(-time.Hour)
	_, err := db.Exec("UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1", p.ID, p.UpdatedAt)
	require.NoError(t, err)
	return p
}
func TestActiveRecoveryLifecycleAndHistoryIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := newActiveTargetPolicy(t, ctx, db, r, now)
	for i := 0; i < 20; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("cold-%d", i), "A", 8, now.Add(-time.Minute+time.Duration(i)*time.Millisecond), 0, true)
	}
	history := activeSamplesForTest(t, ctx, db, p, service.PassiveQualityState{AccountID: 62, ObserveAfter: p.UpdatedAt}, now)
	state := service.PassiveQualityState{Purpose: p.Purpose, PolicyID: p.ID, AccountID: 62, Model: "A", Version: p.DecisionVersion(), Generation: 4, Phase: "paused", NextAt: now, ObserveAfter: p.UpdatedAt, UpdatedAt: now.Add(-time.Minute), Backoff: 2, Evidence: history}
	saveActiveStateForTest(t, ctx, db, state)
	extra := map[string]any{"manual_note": "keep", "scheduled_test_protection": []any{map[string]any{"blocked": true}}}
	extraJSON, err := json.Marshal(extra)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE accounts SET status='quality_paused',extra=$1 WHERE id=62", extraJSON)
	require.NoError(t, err)
	probe, err := r.advance(ctx, p.ID, 62, now, true, "A")
	require.NoError(t, err)
	require.NotNil(t, probe)
	require.Equal(t, "probing", probe.Phase)
	require.NotNil(t, probe.ActiveRecoveryConfig)
	allowed, leased, err := r.AcquirePassiveQuality(ctx, 62, "A", "customer")
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, leased)
	result := activeRecoveryPassingResult(now)
	stale := *probe
	stale.Generation--
	require.NoError(t, r.FinishActiveRecovery(ctx, stale, result, now.Add(123*time.Second)))
	require.Equal(t, "probing", targetStates(t, ctx, r)["A"].Phase)
	require.NoError(t, r.FinishActiveRecovery(ctx, *probe, result, now.Add(123*time.Second)))
	restored := targetStates(t, ctx, r)["A"]
	require.Equal(t, "healthy", restored.Phase)
	require.Equal(t, 20, restored.Evidence.Samples, "active proof must not replace real hourly history")
	require.Equal(t, 0.0, *restored.Evidence.CacheRate)
	require.Equal(t, state.ObserveAfter, restored.ObserveAfter)
	require.Equal(t, now.Add(123*time.Second), restored.RecoveredAt)
	var status, note string
	require.NoError(t, db.QueryRow("SELECT status,extra->>'manual_note' FROM accounts WHERE id=62").Scan(&status, &note))
	require.Equal(t, "quality_paused", status)
	require.Equal(t, "keep", note)
	_, err = r.advance(ctx, p.ID, 62, now.Add(124*time.Second), true, "A")
	require.NoError(t, err)
	require.NotEqual(t, "paused", targetStates(t, ctx, r)["A"].Phase, "old hour cannot immediately repause")
}
func TestActiveRecoveryPendingTimeoutAndInitialVerificationIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := newActiveTargetPolicy(t, ctx, db, r, now)
	probe, err := r.advance(ctx, p.ID, 62, now, true, "A")
	require.NoError(t, err)
	require.NotNil(t, probe, "configured model is actively verified before initial scheduling")
	require.Contains(t, probe.Reason, "initial_verification")
	result := service.PassiveQualityActiveRecoveryResult{StartedAt: now, FinishedAt: now.Add(time.Second), Verdict: "pending", Reason: "metric_unavailable"}
	require.NoError(t, r.FinishActiveRecovery(ctx, *probe, result, now.Add(time.Second)))
	paused := targetStates(t, ctx, r)["A"]
	require.Equal(t, "paused", paused.Phase)
	require.Zero(t, paused.Backoff)
	_, err = db.Exec("UPDATE accounts SET schedulable=false WHERE id=62")
	require.NoError(t, err)
	probe, err = r.advance(ctx, p.ID, 62, now.Add(2*time.Minute), true, "A")
	require.NoError(t, err)
	require.Nil(t, probe, "manual disabled account must not run probes")
	_, err = db.Exec("UPDATE accounts SET schedulable=true WHERE id=62")
	require.NoError(t, err)
	probe, err = r.advance(ctx, p.ID, 62, now.Add(3*time.Minute), true, "A")
	require.NoError(t, err)
	require.NotNil(t, probe)
	_, err = r.advance(ctx, p.ID, 62, probe.NextAt, true, "A")
	require.NoError(t, err)
	paused = targetStates(t, ctx, r)["A"]
	require.Equal(t, "paused", paused.Phase)
	require.Zero(t, paused.Backoff, "worker interruption is not an upstream failure")
}
func TestActiveRecoveryMigrationPreservesThresholdsAndHoldsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 4321, Recover: 3456}
	p.TPS = service.PassiveQualityMetric{Enabled: true, Pause: 27, Recover: 34}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now().UTC().Truncate(time.Microsecond)
	s := service.PassiveQualityState{Purpose: p.Purpose, PolicyID: p.ID, AccountID: 62, Model: "A", Version: p.DecisionVersion(), Generation: 5, Phase: "trial", NextAt: now.Add(time.Minute), ObserveAfter: now.Add(-time.Hour), UpdatedAt: now}
	saveActiveStateForTest(t, ctx, db, s)
	_, err := db.Exec("CREATE TABLE quality_management_config_archive(kind TEXT,source_id BIGINT,config JSONB,PRIMARY KEY(kind,source_id))")
	require.NoError(t, err)
	extra := map[string]any{"manual_note": "preserve", "passive_quality": []any{map[string]any{"policy_id": 999, "phase": "paused", "scope": "account"}}}
	raw, err := json.Marshal(extra)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE accounts SET status='disabled',schedulable=false,extra=$1 WHERE id=62", raw)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/321_group_quality_active_recovery.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	policies, err := r.ListPolicies(ctx)
	require.NoError(t, err)
	require.Len(t, policies, 1)
	updated := policies[0]
	require.True(t, updated.IsActiveRecovery())
	require.Zero(t, updated.MaxSamples)
	require.Equal(t, 3600, updated.WindowSeconds)
	require.Equal(t, p.CacheRate, updated.CacheRate)
	require.Equal(t, p.TTFT, updated.TTFT)
	require.Equal(t, p.TPS, updated.TPS)
	states := targetStates(t, ctx, r)
	require.Equal(t, "paused", states["A"].Phase)
	require.EqualValues(t, 6, states["A"].Generation)
	require.Equal(t, s.ObserveAfter, states["A"].ObserveAfter)
	var status string
	var sched bool
	require.NoError(t, db.QueryRow("SELECT status,schedulable,extra FROM accounts WHERE id=62").Scan(&status, &sched, &raw))
	require.Equal(t, "disabled", status)
	require.False(t, sched)
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.Equal(t, "preserve", extra["manual_note"])
	holds := extra["passive_quality"].([]any)
	require.Len(t, holds, 2)
	found := false
	for _, h := range holds {
		if h.(map[string]any)["recovery_mode"] == "active" {
			found = true
		}
	}
	require.True(t, found, "active holds must remain durably projected after migration")
}

func saveActiveStateForTest(t *testing.T, ctx context.Context, db *sql.DB, s service.PassiveQualityState) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, savePassiveState(ctx, tx, s))
	require.NoError(t, tx.Commit())
}
func TestActiveRecoveryInitialVerificationDoesNotPauseEstablishedQuietAccountIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := newActiveTargetPolicy(t, ctx, db, r, now)
	insertGroupTargetUsage(t, db, r, "previous-hour", "A", 8, now.Add(-2*time.Hour), 90, true)
	probe, err := r.advance(ctx, p.ID, 62, now, true, "A")
	require.NoError(t, err)
	require.Nil(t, probe)
	require.Equal(t, "insufficient", targetStates(t, ctx, r)["A"].Phase)
	p.Model = ""
	require.NoError(t, r.SavePolicy(ctx, &p))
	targets, err := r.modelTargets(ctx, p, now)
	require.NoError(t, err)
	require.Empty(t, targets, "no history within window and no explicit model cannot create an invented model")
}
func TestActiveRecoveryNoOpSavePreservesHoldAndEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := newActiveTargetPolicy(t, ctx, db, r, now)
	state := service.PassiveQualityState{Purpose: p.Purpose, PolicyID: p.ID, AccountID: 62, Model: "A", Version: p.DecisionVersion(), Generation: 8, Phase: "paused", NextAt: now, ObserveAfter: p.UpdatedAt, UpdatedAt: now, Evidence: activeTargetEvidence(now)}
	saveActiveStateForTest(t, ctx, db, state)
	oldVersion := p.Version
	stability := p.GroupTargetStability()
	stability.NewSessionPercent = 100
	p.Stability = &stability
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.Equal(t, oldVersion, p.Version)
	saved := targetStates(t, ctx, r)["A"]
	require.Equal(t, "paused", saved.Phase)
	require.EqualValues(t, 8, saved.Generation)
	require.Equal(t, 20, saved.Evidence.Samples)
}
func TestActiveRecoveryRejectedSuccessDoesNotReportPassedIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := newActiveTargetPolicy(t, ctx, db, r, now)
	probe, err := r.advance(ctx, p.ID, 62, now, true, "A")
	require.NoError(t, err)
	require.NotNil(t, probe)
	result := activeRecoveryPassingResult(now)
	_, err = db.Exec("UPDATE accounts SET schedulable=false WHERE id=62")
	require.NoError(t, err)
	require.NoError(t, r.FinishActiveRecovery(ctx, *probe, result, now.Add(123*time.Second)))
	state := targetStates(t, ctx, r)["A"]
	require.Equal(t, "paused", state.Phase)
	require.NotNil(t, state.ActiveRecovery)
	require.False(t, state.ActiveRecovery.Passed)
	require.Equal(t, "pending", state.ActiveRecovery.Verdict)
	require.Equal(t, "account_unavailable_for_recovery", state.ActiveRecovery.Reason)
}
func TestActiveRecoverySerializesSameAccountModelAcrossGroupsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := newActiveTargetPolicy(t, ctx, db, r, now)
	first, err := r.advance(ctx, p.ID, 62, now, true, "A")
	require.NoError(t, err)
	require.NotNil(t, first)
	_, err = db.Exec("INSERT INTO account_groups(account_id,group_id) VALUES(62,9)")
	require.NoError(t, err)
	other := activeTargetPolicy(t)
	other.GroupIDs = []int64{9}
	require.NoError(t, r.SavePolicy(ctx, &other))
	second, err := r.advance(ctx, other.ID, 62, now, true, "A")
	require.NoError(t, err)
	require.Nil(t, second, "another policy cannot overlap the same upstream model probe")
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, states, 2)
	for _, s := range states {
		if s.PolicyID == other.ID {
			require.Equal(t, "paused", s.Phase)
		}
	}
	result := activeRecoveryPassingResult(now)
	require.NoError(t, r.FinishActiveRecovery(ctx, *first, result, now.Add(123*time.Second)))
	second, err = r.advance(ctx, other.ID, 62, now.Add(124*time.Second), true, "A")
	require.NoError(t, err)
	require.NotNil(t, second)
}
func TestActiveTargetMissingCacheAndNonTextCountersStayExcludedIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	now := time.Now().UTC().Truncate(time.Microsecond)
	p := activeTargetPolicy(t)
	for _, key := range []string{"ordinary", "compact", "unknown", "image", "active-test"} {
		read, reported := int64(90), true
		if key == "unknown" {
			read, reported = 0, false
		}
		insertGroupTargetUsage(t, db, r, key, "A", 8, now.Add(-time.Second), read, reported)
	}
	_, err := db.Exec("UPDATE usage_logs SET native_compaction_v2=true WHERE request_id='compact';UPDATE usage_logs SET image_count=1 WHERE request_id='image';UPDATE usage_logs SET request_type=4 WHERE request_id='active-test'")
	require.NoError(t, err)
	e := activeSamplesForTest(t, ctx, db, p, service.PassiveQualityState{AccountID: 62, ObserveAfter: now.Add(-time.Hour)}, now)
	require.Equal(t, 3, e.Samples)
	require.Equal(t, 1, e.CacheSamples)
	require.Equal(t, 90.0, *e.CacheRate)
	require.Nil(t, e.TTFT, "nonstreaming first_token_ms cannot become TTFT evidence")
	require.Nil(t, e.OutputTPS)
}
