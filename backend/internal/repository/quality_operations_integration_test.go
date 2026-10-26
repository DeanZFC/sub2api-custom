//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func qualityOperationsDB(t *testing.T) (context.Context, *sql.DB, *qualityOperationsRepository, *passiveQualityRepository) {
	ctx, db, passive := passiveDB(t)
	_, err := db.Exec(`CREATE TABLE scheduled_test_plans(id bigint PRIMARY KEY,latest_run_id text,updated_at timestamptz NOT NULL DEFAULT now());INSERT INTO scheduled_test_plans(id,latest_run_id) VALUES(1,'run')`)
	require.NoError(t, err)
	raw, err := os.ReadFile("../../migrations/294_quality_operations.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	raw, err = os.ReadFile("../../migrations/309_quality_recovery_budget_evidence.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	return ctx, db, &qualityOperationsRepository{db: db}, passive
}
func TestQualityOperationsBudgetConcurrentIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	snapshot, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	c := snapshot.Config
	c.BudgetEnabled = true
	c.DailyRequests = 7
	c.AccountDailyRequests = 3
	c.DailyTokens = 0
	c.AccountDailyTokens = 0
	c.TokenReservation = 1024
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	stale := c
	stale.Version--
	require.ErrorIs(t, r.SaveOperationsConfig(ctx, &stale), service.ErrPassiveQualityConflict)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var admitted []*service.QualityProbeReservation
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, e := r.ReserveQualityProbe(ctx, int64(62+i%2), 100, false, "")
			if e != nil {
				var d *service.QualityDeferredError
				require.ErrorAs(t, e, &d)
				return
			}
			mu.Lock()
			admitted = append(admitted, res)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	require.Len(t, admitted, 6)
	snapshot, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 6, snapshot.Usage[0].Requests)
	require.EqualValues(t, 6144, snapshot.Usage[0].Tokens)
	actual := int64(12)
	outcome := service.QualityProbeOutcome{Tokens: &actual, Kind: "healthy"}
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[0], outcome))
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[0], outcome))
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[1], service.QualityProbeOutcome{Kind: "network"}))
	snapshot, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 5132, snapshot.Usage[0].Tokens)
	require.EqualValues(t, 5, snapshot.Usage[0].UnknownRequests)
	// Today's ledger starts independently, while late settlement charges its
	// original reservation day. No result rows are used to calculate budgets.
	_, err = db.Exec(`UPDATE quality_probe_attempts SET day=day-1; UPDATE quality_budget_days SET day=day-1`)
	require.NoError(t, err)
	next, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	require.NotNil(t, next)
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[2], outcome))
	snapshot, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Usage[0].Requests)
	require.EqualValues(t, 1024, snapshot.Usage[0].Tokens)
}

func TestQualityOperationsRecoveryReserveIntegration(t *testing.T) {
	for _, scope := range []string{"global", "account"} {
		for _, meter := range []string{"requests", "tokens"} {
			t.Run(scope+"/"+meter, func(t *testing.T) {
				ctx, _, repo, _ := qualityOperationsDB(t)
				c := service.DefaultQualityOperationsConfig()
				c.BudgetEnabled, c.RecoveryReservePercent, c.TokenReservation = true, 20, 1024
				c.DailyRequests, c.DailyTokens, c.AccountDailyRequests, c.AccountDailyTokens = 0, 0, 0, 0
				switch scope + "/" + meter {
				case "global/requests":
					c.DailyRequests = 10
				case "global/tokens":
					c.DailyTokens = 10240
				case "account/requests":
					c.AccountDailyRequests = 10
				case "account/tokens":
					c.AccountDailyTokens = 10240
				}
				require.NoError(t, repo.SaveOperationsConfig(ctx, &c))
				var wg sync.WaitGroup
				var mu sync.Mutex
				admitted := 0
				for i := 0; i < 30; i++ {
					wg.Add(1)
					go func() {
						defer wg.Done()
						_, err := repo.ReserveQualityProbe(ctx, 62, 100, false, "")
						if err != nil {
							var deferred *service.QualityDeferredError
							require.ErrorAs(t, err, &deferred)
							return
						}
						mu.Lock()
						admitted++
						mu.Unlock()
					}()
				}
				wg.Wait()
				require.Equal(t, 8, admitted)
				recoveryCtx := service.WithQualityRecoveryProbe(ctx)
				first, err := repo.ReserveQualityProbe(recoveryCtx, 62, 100, true, "recovery-a")
				require.NoError(t, err)
				second, err := repo.ReserveQualityProbe(recoveryCtx, 62, 100, true, "recovery-b")
				require.NoError(t, err)
				_, err = repo.ReserveQualityProbe(recoveryCtx, 62, 100, true, "recovery-c")
				require.Error(t, err, "recovery never bypasses the hard cap")
				actual := int64(512)
				outcome := service.QualityProbeOutcome{Tokens: &actual, Kind: "healthy"}
				require.NoError(t, repo.SettleQualityProbe(ctx, first, outcome))
				require.NoError(t, repo.SettleQualityProbe(ctx, first, outcome))
				require.NoError(t, repo.SettleQualityProbe(ctx, second, service.QualityProbeOutcome{Kind: "unknown"}))
				snapshot, err := repo.OperationsSnapshot(ctx)
				require.NoError(t, err)
				require.Len(t, snapshot.Usage, 2)
				for _, usage := range snapshot.Usage {
					require.EqualValues(t, 10, usage.Requests)
					require.EqualValues(t, 2, usage.RecoveryRequests)
					require.EqualValues(t, 9728, usage.Tokens)
					require.EqualValues(t, 1536, usage.RecoveryTokens, "unknown usage keeps its reservation; actual usage settles exactly once")
				}
			})
		}
	}
}
func TestQualityOperationsTokenOverspendAndUnknownIntegration(t *testing.T) {
	ctx, _, r, _ := qualityOperationsDB(t)
	c := service.DefaultQualityOperationsConfig()
	c.BudgetEnabled = true
	c.TokenReservation = 1024
	c.DailyTokens = 2048
	c.AccountDailyTokens = 0
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	first, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	second, err := r.ReserveQualityProbe(ctx, 63, 100, false, "")
	require.NoError(t, err)
	_, err = r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.Error(t, err)
	require.NoError(t, r.SettleQualityProbe(ctx, first, service.QualityProbeOutcome{Kind: "server"}))
	_, err = r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.Error(t, err, "missing usage must retain reservation")
	actual := int64(4096)
	require.NoError(t, r.SettleQualityProbe(ctx, second, service.QualityProbeOutcome{Tokens: &actual, Kind: "healthy"}))
	_, err = r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.Error(t, err, "reported usage above reserve stops later requests")
	s, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 5120, s.Usage[0].Tokens)
}
func TestQualityOperationsSupplierIsolationIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	supplier := service.QualitySupplier{Name: "vendor", Enabled: true, AccountIDs: []int64{62, 63}, WindowSeconds: 600, MinAccounts: 2, FailurePercent: 60, ProbeIntervalSeconds: 60}
	require.NoError(t, r.SaveSupplier(ctx, &supplier))
	duplicate := supplier
	duplicate.ID = 0
	duplicate.Version = 0
	require.Error(t, r.SaveSupplier(ctx, &duplicate))
	var first *service.QualityProbeReservation
	for _, id := range supplier.AccountIDs {
		res, err := r.ReserveQualityProbe(ctx, id, 100, false, "")
		require.NoError(t, err)
		if first == nil {
			first = res
		}
		require.NoError(t, r.SettleQualityProbe(ctx, res, service.QualityProbeOutcome{Kind: "server"}))
	}
	s, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.True(t, s.Suppliers[0].Incident)
	sentinel, err := r.ReserveQualityProbe(ctx, 62, 100, true, "round-1")
	require.NoError(t, err)
	_, err = r.ReserveQualityProbe(ctx, 63, 100, true, "round-2")
	require.Error(t, err)
	_, err = r.ReserveQualityProbe(ctx, 62, 100, true, "round-1")
	require.NoError(t, err, "sentinel can finish a serial suite")
	_, err = r.ReserveQualityProbe(ctx, 63, 100, false, "manual")
	require.NoError(t, err, "manual run bypasses supplier cooldown")
	require.NoError(t, r.SettleQualityProbe(ctx, sentinel, service.QualityProbeOutcome{Kind: "healthy"}))
	s, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.False(t, s.Suppliers[0].Incident)
	require.Equal(t, 1, s.Suppliers[0].Affected)
	var stopped int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM accounts WHERE NOT schedulable OR status<>'active'`).Scan(&stopped))
	require.Zero(t, stopped, "aggregation must not own peer scheduling status")
	// Authentication failures cannot establish a common supplier outage.
	res, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	require.NoError(t, r.SettleQualityProbe(ctx, res, service.QualityProbeOutcome{Kind: "auth"}))
	s, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.False(t, s.Suppliers[0].Incident)
	// Config edits fence in-flight old observations and invalidate old evidence.
	old, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	require.NoError(t, r.SaveSupplier(ctx, &supplier))
	require.NoError(t, r.SettleQualityProbe(ctx, old, service.QualityProbeOutcome{Kind: "server"}))
	s, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.Zero(t, s.Suppliers[0].Observed)
	require.NoError(t, r.DeleteSupplier(ctx, supplier.ID, supplier.Version))
	_, err = r.ReserveQualityProbe(ctx, 63, 100, true, "after-delete")
	require.NoError(t, err)
}
func TestQualityOperationsHealthyFrequencyFencingIntegration(t *testing.T) {
	ctx, db, r, passive := qualityOperationsDB(t)
	c := service.DefaultQualityOperationsConfig()
	c.DynamicEnabled = true
	c.HealthyRounds = 2
	c.HealthyBaseSeconds = 60
	c.HealthyMaxSeconds = 240
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	plan := &service.ScheduledTestPlan{ID: 1}
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM scheduled_test_plans WHERE id=1`).Scan(&plan.UpdatedAt))
	id := int64(62)
	result := &service.ScheduledTestResult{AccountID: &id, RunID: "run", StartedAt: time.Now().Add(-time.Minute)}
	for i := 0; i < 2; i++ {
		result.StartedAt = result.StartedAt.Add(time.Second)
		require.NoError(t, r.RecordQualityFrequency(ctx, plan, result, "policy", true))
	}
	next, err := r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.NotNil(t, next)
	next, err = r.QualityFrequencyDue(ctx, 1, id, "edited-policy")
	require.NoError(t, err)
	require.Nil(t, next)
	stale := *result
	stale.RunID = "old"
	stale.StartedAt = time.Now()
	require.NoError(t, r.RecordQualityFrequency(ctx, plan, &stale, "policy", false))
	next, err = r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.NotNil(t, next)
	p := newPassivePolicy()
	require.NoError(t, passive.SavePolicy(ctx, &p))
	_, err = db.Exec(`INSERT INTO passive_quality_states(policy_id,account_id,data) VALUES($1,62,jsonb_build_object('phase','suspect','updated_at',now()))`, p.ID)
	require.NoError(t, err)
	next, err = r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.Nil(t, next, "fresh passive anomalies cancel healthy frequency")
	result.StartedAt = time.Now()
	require.NoError(t, r.RecordQualityFrequency(ctx, plan, result, "policy", true))
	// A clock offset must not turn a reset streak into a healthy deferral.
	_, err = db.Exec(`UPDATE quality_healthy_frequency SET next_at=now()+INTERVAL '1 hour' WHERE plan_id=1 AND account_id=62`)
	require.NoError(t, err)
	next, err = r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.Nil(t, next, "active success cannot slow tests of a passive-degraded account")
}
func TestQualityOperationsReplayReadOnlyAndFreshEvidenceIntegration(t *testing.T) {
	ctx, db, r, passive := qualityOperationsDB(t)
	from := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i := 0; i < 7; i++ {
		at := from.Add(-time.Second)
		if i >= 5 {
			at = from.Add(30 * time.Second)
		}
		require.NoError(t, passive.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: fmt.Sprintf("replay-%d", i), StartedAt: at, At: at}}, nil))
	}
	p := newPassivePolicy()
	req := service.QualityReplayRequest{Config: p.PassiveQualityConfig, AccountIDs: []int64{62}, From: from, To: from.Add(time.Minute), StepSeconds: 15}
	result, err := r.ReplayQualityPolicy(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, result.Triggers)
	require.Equal(t, 5, result.Evaluations)
	require.Empty(t, result.Points[0].Action)
	require.Empty(t, result.Points[1].Action)
	require.Equal(t, 1, result.Points[1].Hits)
	require.Equal(t, "would_pause", result.Points[2].Action)
	require.Equal(t, "recovery_requires_probe", result.Points[3].Action)
	for _, table := range []string{"passive_quality_states", "passive_quality_events", "passive_quality_policies", "quality_probe_attempts", "quality_budget_days"} {
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, table)
	}
	// A future telemetry gap cannot contaminate an earlier replay window.
	gap := from.Add(45 * time.Second)
	require.NoError(t, passive.RecordFailures(ctx, nil, &gap))
	result, err = r.ReplayQualityPolicy(ctx, req)
	require.NoError(t, err)
	require.True(t, result.Points[0].Evidence.Complete)
	require.False(t, result.Points[3].Evidence.Complete)
	require.Equal(t, "pending", result.Points[3].Verdict)
	req.Config.ExcludedAccountIDs = []int64{62}
	_, err = r.ReplayQualityPolicy(ctx, req)
	require.Error(t, err)
}

func TestQualityOperationsReplayAllModelsIntegration(t *testing.T) {
	ctx, db, r, passive := qualityOperationsDB(t)
	from := time.Now().UTC().Truncate(time.Second).Add(-time.Hour)
	for i := 0; i < 7; i++ {
		at := from.Add(-time.Second)
		if i >= 5 {
			at = from.Add(30 * time.Second)
		}
		require.NoError(t, passive.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: fmt.Sprintf("model-%d", i%2), RequestKey: fmt.Sprintf("mixed-%d", i), StartedAt: at, At: at}}, nil))
	}
	p := newPassivePolicy()
	p.Model = ""
	req := service.QualityReplayRequest{Config: p.PassiveQualityConfig, AccountIDs: []int64{62}, From: from, To: from.Add(time.Minute), StepSeconds: 15}
	result, err := r.ReplayQualityPolicy(ctx, req)
	require.NoError(t, err)
	require.Equal(t, 1, result.Triggers)
	require.Equal(t, 5, result.Points[0].Evidence.Samples)
	require.Equal(t, "would_pause", result.Points[2].Action)
	req.Config.Model = "model-0"
	filtered, err := r.ReplayQualityPolicy(ctx, req)
	require.NoError(t, err)
	require.Zero(t, filtered.Triggers, "each model alone has insufficient samples")
	require.Equal(t, 3, filtered.Points[0].Evidence.Samples)
	for _, table := range []string{"passive_quality_states", "passive_quality_events", "passive_quality_policies", "quality_probe_attempts", "quality_budget_days"} {
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table).Scan(&count))
		require.Zero(t, count, table)
	}
}

func TestQualityOperationsDeferredPassiveProbeKeepsHoldAndBackoffIntegration(t *testing.T) {
	ctx, _, _, r := qualityOperationsDB(t)
	p := newPassivePolicy()
	p.Confirmations = 1
	require.NoError(t, r.SavePolicy(ctx, &p))
	at := time.Now()
	for i := 0; i < 5; i++ {
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: fmt.Sprintf("defer-%d", i), StartedAt: at, At: at}}, nil))
	}
	_, err := r.advance(ctx, p.ID, 62, at.Add(time.Second), true)
	require.NoError(t, err)
	require.NoError(t, r.Act(ctx, p.ID, 62, "verify", 0))
	probe, err := r.advance(ctx, p.ID, 62, time.Now().Add(2*time.Second), true)
	require.NoError(t, err)
	require.NotNil(t, probe)
	until := time.Now().Add(time.Hour)
	require.NoError(t, r.PostponePassiveProbe(ctx, *probe, until))
	require.NoError(t, r.FinishProbe(ctx, *probe, true, time.Now().Add(3*time.Second)))
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, "paused", states[0].Phase)
	require.Equal(t, probe.Backoff, states[0].Backoff)
	require.WithinDuration(t, until, states[0].NextAt, time.Millisecond)
	ok, err := acquirePassiveForTest(r, ctx, 62, "model", "deferred")
	require.NoError(t, err)
	require.False(t, ok)
}

func TestQualityOperationsRetentionPreservesCurrentLedgerIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	old, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE quality_probe_attempts SET started_at=now()-INTERVAL '31 days' WHERE id=$1`, old.ID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO quality_budget_days(day,account_id,requests,tokens,unknown_requests) VALUES((now() AT TIME ZONE 'UTC')::date-91,0,1,1024,1)`)
	require.NoError(t, err)
	require.NoError(t, r.PruneQualityOperations(ctx))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM quality_probe_attempts`).Scan(&count))
	require.Zero(t, count)
	snapshot, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Usage[0].Requests)
	require.EqualValues(t, 32768, snapshot.Usage[0].Tokens)
}
