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

func saveGroupTargetWaitingTrial(t *testing.T, ctx context.Context, db *sql.DB, s service.PassiveQualityState) {
	t.Helper()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, savePassiveState(ctx, tx, s))
	require.NoError(t, reconcilePassiveAccount(ctx, tx, s.AccountID))
	require.NoError(t, tx.Commit())
}

func TestGroupQualityTargetTrialWaitingAdmissionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.TrialSeconds, p.CooldownSeconds, p.TrialRequests = 60, 30, 5
	stability := p.GroupTargetStability()
	stability.RecoverySeconds = 0
	p.Stability = &stability
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: "A", Scope: p.Scope, Mode: p.Mode, Version: p.DecisionVersion(), Phase: "trial", Generation: 7, Backoff: 3, Used: 5, TrialStartedAt: now.Add(-2 * time.Minute), TrialEndsAt: now.Add(-time.Second), LastEvidenceAt: now.Add(-2 * time.Minute), UpdatedAt: now}
	saveGroupTargetWaitingTrial(t, ctx, db, s)
	_, err := r.advance(ctx, p.ID, 62, now, false, "A")
	require.NoError(t, err)
	s = targetStates(t, ctx, r)["A"]
	require.Equal(t, "trial", s.Phase)
	require.Equal(t, "waiting_for_recovery: insufficient_samples", s.Reason)
	require.EqualValues(t, 7, s.Generation)
	require.Equal(t, 3, s.Backoff)
	require.Equal(t, 5, s.Used)
	allowed, leased, err := r.AcquirePassiveQuality(ctx, 62, "A", "cooldown")
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, leased)

	// A process crash can leave an unexpired-generation lease without a release.
	// The next bounded window must fence it, rather than wait forever.
	s.TrialEndsAt = now.Add(-31 * time.Second)
	s.Inflight = 1
	saveGroupTargetWaitingTrial(t, ctx, db, s)
	_, err = db.Exec(`INSERT INTO passive_quality_leases(id,policy_id,account_id,model_key,generation) VALUES('orphan',$1,62,'A',7)`, p.ID)
	require.NoError(t, err)
	_, err = r.advance(ctx, p.ID, 62, now, false, "A")
	require.NoError(t, err)
	s = targetStates(t, ctx, r)["A"]
	require.EqualValues(t, 8, s.Generation)
	require.Equal(t, 3, s.Backoff)
	require.Zero(t, s.Used)
	require.Zero(t, s.Inflight)
	require.Equal(t, now.Add(-2*time.Minute).UnixMicro(), s.TrialStartedAt.UnixMicro())
	require.WithinDuration(t, now.Add(p.GroupTargetTrialDuration()), s.TrialEndsAt, time.Microsecond)
	allowed, leased, err = r.AcquirePassiveQuality(ctx, 62, "A", "new-first")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, leased)
	allowed, _, err = r.AcquirePassiveQuality(ctx, 62, "A", "new-concurrent")
	require.NoError(t, err)
	require.False(t, allowed, "reopened trials retain the configured concurrency gate")
	require.NoError(t, r.ReleasePassiveQuality(ctx, "new-first", false))

	// A delayed old-generation unused refund cannot debit the new window.
	require.NoError(t, r.ReleasePassiveQuality(ctx, "orphan", true))
	require.Equal(t, 1, targetStates(t, ctx, r)["A"].Used)
	for i := 1; i < p.TrialRequests; i++ {
		// Simulate a sweep consuming the preceding batch. This fixture
		// isolates lease generations and caps; pacing has separate tests.
		observed := targetStates(t, ctx, r)["A"]
		observed.LastTrialObservedAt = time.Now()
		saveGroupTargetWaitingTrial(t, ctx, db, observed)
		key := fmt.Sprintf("bounded-%d", i)
		allowed, leased, err = r.AcquirePassiveQuality(ctx, 62, "A", key)
		require.NoError(t, err)
		require.True(t, allowed)
		require.True(t, leased)
		require.NoError(t, r.ReleasePassiveQuality(ctx, key, false))
	}
	allowed, _, err = r.AcquirePassiveQuality(ctx, 62, "A", "over-budget")
	require.NoError(t, err)
	require.False(t, allowed, "reopened trials retain the total request cap")

	// A second empty cycle is not a failure and cannot bypass another hold.
	s = targetStates(t, ctx, r)["A"]
	s.TrialEndsAt = now.Add(-31 * time.Second)
	saveGroupTargetWaitingTrial(t, ctx, db, s)
	_, err = db.Exec(`UPDATE accounts SET schedulable=false WHERE id=62`)
	require.NoError(t, err)
	_, err = r.advance(ctx, p.ID, 62, now, false, "A")
	require.NoError(t, err)
	s = targetStates(t, ctx, r)["A"]
	require.EqualValues(t, 8, s.Generation)
	require.Equal(t, p.TrialRequests, s.Used)
	_, err = db.Exec(`UPDATE accounts SET schedulable=true WHERE id=62`)
	require.NoError(t, err)
	_, err = r.advance(ctx, p.ID, 62, now, false, "A")
	require.NoError(t, err)
	s = targetStates(t, ctx, r)["A"]
	require.EqualValues(t, 9, s.Generation)
	require.Equal(t, "trial", s.Phase)
	require.Equal(t, 3, s.Backoff)
	require.Zero(t, s.Used)
	var events int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM passive_quality_events WHERE policy_id=$1`, p.ID).Scan(&events))
	require.Zero(t, events, "empty cycles must not emit repeated pause/trial events")
}

func TestGroupQualityTargetLegacyTrialBudgetSavePreservesEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	// This shape was accepted by older editors: the requested fresh batch and
	// confirmations exceed the finite trial budget. Runtime normalization keeps
	// the trial bounded instead of rejecting an otherwise valid policy update.
	p.NewSamples, p.Confirmations, p.TrialRequests, p.RecoverySamples = 20, 10, 5, 5
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.Equal(t, 20, p.NewSamples)
	require.Equal(t, 10, p.Confirmations)
	require.Equal(t, 5, p.TrialRequests)
	require.Equal(t, 5, p.GroupTargetRecoveryConfirmations())
	require.Equal(t, 1, p.GroupTargetRecoveryBatchSize())

	now := time.Now()
	state := service.PassiveQualityState{
		PolicyID: p.ID, PolicyName: p.Name, Purpose: p.Purpose, AccountID: 62,
		Model: "A", Scope: p.Scope, Mode: p.Mode, Version: p.Version,
		Phase: "suspect", Generation: 4, Hits: 1, Backoff: 2,
		LastEvidenceAt: now.Add(-time.Minute), ObserveAfter: p.EvidenceSince(),
		UpdatedAt: now,
		Evidence:  service.PassiveQualityEvidence{Samples: 7, Fresh: 2, CacheSamples: 7},
	}
	saveGroupTargetWaitingTrial(t, ctx, db, state)

	before, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, before, 1)
	// A no-op save must not reset the decision identity or evidence boundary.
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.Equal(t, before[0], targetStates(t, ctx, r)["A"])

	oldVersion, oldDecision := p.Version, p.DecisionVersion()
	p.Name = "renamed legacy target"
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.Equal(t, oldVersion+1, p.Version)
	require.Equal(t, oldDecision, p.DecisionVersion())
	after := targetStates(t, ctx, r)["A"]
	require.Equal(t, "renamed legacy target", after.PolicyName)
	require.Equal(t, before[0].Evidence, after.Evidence)
	require.Equal(t, before[0].Generation, after.Generation)
	require.Equal(t, before[0].Backoff, after.Backoff)
}

func TestGroupQualityTargetTrialWaitingEvidenceWindowIntegration(t *testing.T) {
	for _, read := range []int64{50, 95} {
		t.Run(fmt.Sprintf("cache_%d", read), func(t *testing.T) {
			ctx, db, r := passiveDB(t)
			p := newGroupTargetPolicy(t, ctx, db, r)
			p.WindowSeconds, p.TrialSeconds, p.CooldownSeconds = 60, 60, 30
			require.NoError(t, r.SavePolicy(ctx, &p))
			now := time.Now()
			start := now.Add(-10 * time.Minute)
			s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: "A", Scope: p.Scope, Mode: p.Mode, Version: p.DecisionVersion(), Phase: "trial", Generation: 7, Backoff: 3, Used: 2, TrialStartedAt: start, TrialEndsAt: now.Add(-31 * time.Second), LastEvidenceAt: start, UpdatedAt: now}
			saveGroupTargetWaitingTrial(t, ctx, db, s)
			for i := 0; i < 5; i++ {
				insertGroupTargetUsage(t, db, r, fmt.Sprintf("old-%d", i), "A", 8, now.Add(-5*time.Minute), 95, true)
			}
			for i := 0; i < 2; i++ {
				insertGroupTargetUsage(t, db, r, fmt.Sprintf("recent-%d", i), "A", 8, now.Add(-20*time.Second), read, true)
			}
			insertGroupTargetUsage(t, db, r, "pre-trial-late", "A", 8, now.Add(-time.Second), 95, true)
			_, err := db.Exec(`UPDATE usage_logs SET duration_ms=660000 WHERE request_id='pre-trial-late'`)
			require.NoError(t, err)
			_, err = r.advance(ctx, p.ID, 62, now, false, "A")
			require.NoError(t, err)
			s = targetStates(t, ctx, r)["A"]
			require.Equal(t, "trial", s.Phase, "expired and pre-trial evidence cannot satisfy recovery")
			require.EqualValues(t, 8, s.Generation)
			require.Equal(t, 2, s.Evidence.Samples)
			require.Equal(t, 2, s.Evidence.CacheSamples)
			require.Equal(t, 3, s.Backoff)
			for i := 0; i < 3; i++ {
				insertGroupTargetUsage(t, db, r, fmt.Sprintf("new-%d", i), "A", 8, now.Add(time.Second), read, true)
			}
			_, err = r.advance(ctx, p.ID, 62, now.Add(2*time.Second), false, "A")
			require.NoError(t, err)
			s = targetStates(t, ctx, r)["A"]
			require.Equal(t, 5, s.Evidence.Samples, "recent samples survive renewal until the rolling window expires")
			require.Equal(t, "trial", s.Phase, "one fresh batch cannot skip the recovery duration, irrespective of cache rate")
			require.Equal(t, 3, s.Backoff)
			// Recovery must remain supported by fresh traffic; a gap longer
			// than the paced trial evidence allowance restarts its clock.
			for i := 0; i < 2; i++ {
				insertGroupTargetUsage(t, db, r, fmt.Sprintf("middle-%d", i), "A", 8, now.Add(61*time.Second+time.Duration(i)*time.Millisecond), read, true)
			}
			_, err = r.advance(ctx, p.ID, 62, now.Add(62*time.Second), false, "A")
			require.NoError(t, err)
			require.Equal(t, "trial", targetStates(t, ctx, r)["A"].Phase)
			for i := 0; i < 2; i++ {
				insertGroupTargetUsage(t, db, r, fmt.Sprintf("final-%d", i), "A", 8, now.Add(122*time.Second+time.Duration(i)*time.Millisecond), read, true)
			}
			_, err = r.advance(ctx, p.ID, 62, now.Add(123*time.Second), false, "A")
			require.NoError(t, err)
			s = targetStates(t, ctx, r)["A"]
			if read == 95 {
				require.Equal(t, "healthy", s.Phase)
			} else {
				require.Equal(t, "suspect", s.Phase, "cache remains a soft allocation restriction after hard recovery")
			}
			require.Equal(t, 3, s.Backoff, "successful trial must preserve repeated-failure backoff")
			require.Equal(t, start.UnixMicro(), s.ObserveAfter.UnixMicro())
		})
	}
}

func TestGroupQualityTargetTrialRetainsAllRequiredRecoveryBatchesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.CacheRate.Enabled = false
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 5}
	p.WindowSeconds, p.TrialSeconds = 60, 60
	p.Confirmations, p.NewSamples = 10, 2
	p.TrialRequests, p.RecoverySamples = 20, 20
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := time.Now().Add(-10 * time.Minute)
	_, err := db.Exec(`UPDATE passive_quality_policies SET updated_at=$2 WHERE id=$1`, p.ID, base.Add(-time.Second))
	require.NoError(t, err)
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, Purpose: p.Purpose, AccountID: 62, Model: "A", Scope: p.Scope, Mode: p.Mode, Version: p.DecisionVersion(), UpdatedAt: base}
	beginPassiveTrial(p, &s, base, "group_target_trial")
	saveGroupTargetWaitingTrial(t, ctx, db, s)
	for batch := 0; batch < 10; batch++ {
		for sample := 0; sample < 2; sample++ {
			at := base.Add(time.Duration(batch*30+sample+1) * time.Second)
			insertGroupTargetUsage(t, db, r, fmt.Sprintf("recovery-%d-%d", batch, sample), "A", 8, at, 95, true)
		}
		_, err = r.advance(ctx, p.ID, 62, base.Add(time.Duration((batch+1)*30)*time.Second), false, "A")
		require.NoError(t, err)
		s = targetStates(t, ctx, r)["A"]
		require.Equal(t, (batch+1)*2, s.Evidence.Samples, "SQL evidence window must retain early batches beyond the ordinary window")
		if batch < 9 {
			require.Equal(t, "trial", s.Phase, "all required recovery samples remain mandatory")
		}
	}
	require.Equal(t, "healthy", s.Phase, "finite successful trial must be able to reach recovery")
}
