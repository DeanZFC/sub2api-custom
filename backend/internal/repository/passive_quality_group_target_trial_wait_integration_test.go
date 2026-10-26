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
	require.WithinDuration(t, now.Add(time.Minute), s.TrialEndsAt, time.Microsecond)
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
				insertGroupTargetUsage(t, db, r, fmt.Sprintf("old-%d", i), "A", 8, now.Add(-2*time.Minute), 95, true)
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
			if read == 95 {
				require.Equal(t, "healthy", s.Phase)
				require.Zero(t, s.Backoff)
				require.Equal(t, start.UnixMicro(), s.ObserveAfter.UnixMicro())
			} else {
				require.Equal(t, "paused", s.Phase)
				require.Contains(t, s.Reason, "trial_failed:")
				require.Equal(t, 4, s.Backoff)
			}
		})
	}
}
