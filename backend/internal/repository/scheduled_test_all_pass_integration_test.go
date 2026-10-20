//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type allPassFixture struct {
	plan     *service.ScheduledTestPlan
	round    func(string, time.Time) []*service.ScheduledTestResult
	complete func(*service.ScheduledTestResult, string)
	check    func(string, bool, ...int64)
	review   func(*service.ScheduledTestResult, string)
}

func newAllPassFixture(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, recovery bool, thresholds ...service.ScheduledTestThreshold) allPassFixture {
	t.Helper()
	ids := make([]int64, 4)
	for i, key := range []string{"candy", "model_check", "hourly_stats", "pelican"} {
		require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key=$1`, key).Scan(&ids[i]))
	}
	plan := genericRoutingPlan(ids[0], 8, 10)
	plan.Protection.Mode, plan.Protection.PassGroupIDs = "all_pass", []int64{10}
	plan.TestDefinitionIDs = ids
	plan.Protection.Rules = append(plan.Protection.Rules,
		service.ScheduledTestProtectionRule{TestDefinitionID: ids[1], ModelMatch: "exact"},
		service.ScheduledTestProtectionRule{TestDefinitionID: ids[2], MinSamples: 2, Thresholds: []service.ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 80}}},
		service.ScheduledTestProtectionRule{TestDefinitionID: ids[3], Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}},
	)
	if recovery {
		plan.Protection.Rules[2].Recovery = &service.ScheduledTestCacheRecoveryConfig{Enabled: true, CooldownSeconds: 60, TrialSeconds: 60, MaxRequests: 3, MinSamples: 2, RecoverRate: 90}
	}
	if len(thresholds) > 0 {
		plan.Protection.Rules[2].Thresholds = thresholds
	}
	// Repository fixtures bypass service validation; supply its canonical actions.
	for i := range plan.Protection.Rules {
		rule := &plan.Protection.Rules[i]
		rule.PauseOnFailure = true
		rule.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
		rule.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
	}
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	f := allPassFixture{plan: plan}
	f.round = func(run string, at time.Time) []*service.ScheduledTestResult {
		t.Helper()
		inputs := genericRoundInputs(plan, []int64{62}, at)
		for i, row := range inputs {
			row.OutputKind = []string{"number", "model_check", "statistics", "html"}[i]
		}
		rows, err := repo.BeginRun(ctx, plan, run, inputs)
		require.NoError(t, err)
		require.NoError(t, repo.BeginProtectionRun(ctx, plan, at))
		for i, row := range rows {
			require.NoError(t, repo.BeginProtection(ctx, row, plan.Protection.Rules[i]))
		}
		return rows
	}
	f.complete = func(row *service.ScheduledTestResult, verdict string) {
		t.Helper()
		row.Status = "success"
		if row.OutputKind == "html" {
			row.OutputHTML = "<svg/>"
		}
		require.NoError(t, repo.Update(ctx, row))
		require.NoError(t, repo.CompleteProtection(ctx, row, verdict, "fixture "+verdict))
	}
	f.check = func(status string, trial bool, groups ...int64) {
		t.Helper()
		var got string
		var marked bool
		var memberships pq.Int64Array
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status,COALESCE(extra->>'quality_protection_trial'='true',FALSE) FROM accounts WHERE id=62`).Scan(&got, &marked))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&memberships))
		require.Equal(t, status, got)
		require.Equal(t, trial, marked)
		require.Equal(t, groups, []int64(memberships))
	}
	f.review = func(row *service.ScheduledTestResult, verdict string) {
		t.Helper()
		var generation int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT generation FROM scheduled_test_protection_states WHERE result_id=$1`, row.ID).Scan(&generation))
		require.NoError(t, repo.DecideTestResult(ctx, 1, row.ID, generation, verdict))
	}
	return f
}

func testScheduledTestAllPassLifecycle(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository) {
	f := newAllPassFixture(t, ctx, db, plans, repo, false)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows := f.round("allpass-first", started)
	f.check("active", false, 8, 9)
	f.complete(rows[0], "pass")
	f.complete(rows[1], "pass")
	f.check("active", false, 8, 9) // No group promotion before statistics pass.
	f.complete(rows[2], "fail")
	f.check("quality_paused", false, 8, 9)
	holds, err := repo.ListProtectionHolds(ctx, 62)
	require.NoError(t, err)
	require.Len(t, holds, 1)
	require.True(t, holds[0].Config.UsesAllPass())
	require.Len(t, holds[0].States, 4)
	f.complete(rows[3], "pass") // Automated execution of a review is not approval.
	f.review(rows[3], "pass")
	f.check("quality_paused", false, 8, 9)
	next := f.round("allpass-next", started.Add(time.Minute))
	f.check("quality_paused", false, 8, 9)
	f.complete(next[2], "pass")
	f.complete(next[1], "pass")
	f.check("quality_paused", false, 8, 9)
	f.complete(next[0], "pass")
	f.check("active", false, 10) // Pending Pelican does not block automatic release.
	f.complete(next[3], "fail")
	f.check("active", false, 10) // Only an administrator can veto a manual review.
	f.review(next[3], "fail")
	f.check("quality_paused", false, 10)
	f.review(next[3], "pass")
	f.check("active", false, 10)
	f.review(next[3], "fail")
	third := f.round("allpass-third", started.Add(2*time.Minute))
	f.complete(third[0], "pass")
	f.complete(third[1], "pass")
	f.check("quality_paused", false, 10)
	f.complete(third[2], "pass")
	f.check("active", false, 10) // New round invalidates the old manual veto.
	require.NoError(t, repo.CompleteProtection(ctx, rows[0], "fail", "stale run"))
	f.check("active", false, 10)
	// Runtime group ownership must include the simplified mode's destination.
	other := genericRoutingPlan(*rows[0].TestDefinitionID, 12, 13)
	other.Protection.Mode = "all_pass"
	other.Protection.PassGroupIDs = []int64{13}
	_, err = plans.Create(ctx, other)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES(62,12)`)
	require.NoError(t, err)
	eligible, err := repo.IsPlanRunAccountEligible(ctx, f.plan.ID, "allpass-third", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	require.ErrorContains(t, repo.CompleteProtection(ctx, third[0], "fail", ""), "conflicting enabled test strategies")
}

func testScheduledTestAllPassCustomActions(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository) {
	t.Helper()
	f := newAllPassFixture(t, ctx, db, plans, repo, false)
	f.plan.Protection.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{10}}
	f.plan.Protection.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{8}}
	updated, err := plans.Update(ctx, f.plan)
	require.NoError(t, err)
	require.Equal(t, f.plan.Protection.OnPass, updated.Protection.OnPass)
	require.Equal(t, f.plan.Protection.OnFail, updated.Protection.OnFail)
	f.plan = updated

	started := time.Now().UTC().Truncate(time.Microsecond)
	failed := f.round("allpass-custom-fail", started)
	f.complete(failed[0], "fail")
	f.check("active", false, 8) // A configured keep action leaves scheduling active and assigns failure groups.

	passed := f.round("allpass-custom-pass", started.Add(time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(passed[i], "pass")
	}
	f.check("quality_paused", false, 10) // A configured pause action and independent pass groups take effect together.
	f.complete(passed[3], "pass")
	f.review(passed[3], "fail")
	f.check("quality_paused", false, 8) // Keep scheduling preserves the policy's existing hold.

	// Both sides of runtime ownership must recognize the new outcome actions,
	// including a strategy with group routing only on failure.
	other := genericRoutingPlan(*passed[0].TestDefinitionID, 12, 13)
	other.Protection.Mode = "all_pass"
	other.Protection.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}
	other.Protection.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{13}}
	_, err = plans.Create(ctx, other)
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES(62,12)`)
	require.NoError(t, err)
	eligible, err := repo.IsPlanRunAccountEligible(ctx, f.plan.ID, "allpass-custom-pass", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	require.ErrorContains(t, repo.CompleteProtection(ctx, passed[0], "fail", ""), "conflicting enabled test strategies")
}

func testScheduledTestAllPassCustomRecoveryGroups(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(allPassStatisticsFixture)
	t.Cleanup(func() { exec(`DROP TABLE usage_logs,ops_error_logs`) })
	f := newAllPassFixture(t, ctx, db, plans, repo, true)
	f.plan.Protection.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{10}}
	f.plan.Protection.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{8}}
	_, err := plans.Update(ctx, f.plan)
	require.NoError(t, err)
	now := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Microsecond)
	rows := f.round("allpass-custom-recovery", now.Add(-time.Minute))
	f.complete(rows[2], "fail")
	f.check("quality_paused", false, 8)
	exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1 AND recovery_phase='cooldown'`, f.plan.ID, now.Add(-time.Second))
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
	f.check("quality_paused", false, 8)
	f.complete(rows[0], "pass")
	f.complete(rows[1], "pass")
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
	f.check("active", true, 8)
	exec(`INSERT INTO usage_logs(created_at,input_tokens,cache_read_tokens) VALUES($1,10,90),($1,5,95)`, now.Add(time.Second))
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(5*time.Second)))
	f.check("active", false, 10)
}

func testScheduledTestAllPassRecovery(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, threshold service.ScheduledTestThreshold) {
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(allPassStatisticsFixture)
	t.Cleanup(func() { exec(`DROP TABLE usage_logs,ops_error_logs`) })
	f := newAllPassFixture(t, ctx, db, plans, repo, true, threshold)
	now := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Microsecond)
	rows := f.round("allpass-recovery", now.Add(-time.Minute))
	f.complete(rows[2], "fail")
	f.check("quality_paused", false, 8, 9)
	exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1 AND recovery_phase='cooldown'`, f.plan.ID, now.Add(-time.Second))
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
	f.check("quality_paused", false, 8, 9) // Pending model/candy prevent trial traffic.
	f.complete(rows[0], "pass")
	f.complete(rows[1], "pass")
	rows = f.round("allpass-recovery-next", now.Add(-30*time.Second))
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
	f.check("quality_paused", false, 8, 9) // Previous-round passes cannot open a new trial.
	f.complete(rows[0], "pass")
	f.complete(rows[1], "pass")
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
	f.check("active", true, 8, 9) // Opening trial must not promote groups.
	f.complete(rows[3], "pass")
	f.review(rows[3], "fail")
	f.check("quality_paused", false, 8, 9)
	f.review(rows[3], "pass")
	f.check("active", true, 8, 9)
	// Final request admission still enforces the trial budget.
	admission := &accountRepository{sql: db}
	for i := 0; i < 3; i++ {
		allowed, err := admission.AcquireCacheRecoveryRequest(ctx, 62)
		require.NoError(t, err)
		require.True(t, allowed)
	}
	allowed, err := admission.AcquireCacheRecoveryRequest(ctx, 62)
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(time.Second)))
	f.check("active", true, 8, 9) // No fresh statistics, no full release.
	if threshold.Metric == "cache_rate" {
		exec(`INSERT INTO usage_logs(created_at,input_tokens,cache_read_tokens) VALUES($1,10,90),($1,5,95)`, now.Add(time.Second))
	} else {
		// Success-rate and latency recovery must work without any cache samples.
		exec(`INSERT INTO usage_logs(created_at,input_tokens,request_type,stream,first_token_ms) VALUES($1,10,1,FALSE,500),($1,10,1,FALSE,1000)`, now.Add(time.Second))
	}
	require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(5*time.Second)))
	f.check("active", false, 10)

	// A later pause can recover on a new round's explicit probe with no usage.
	rows = f.round("allpass-probe-pause", now.Add(10*time.Second))
	rows[2].OutputStatistics = &service.ScheduledTestStatistics{WindowStart: now, WindowEnd: rows[2].StartedAt}
	f.complete(rows[2], "fail")
	f.check("quality_paused", false, 10)
	rows = f.round("allpass-probe-recover", now.Add(20*time.Second))
	needed, err := repo.StatisticsRecoveryProbeNeeded(ctx, rows[2])
	require.NoError(t, err)
	require.True(t, needed)
	rows[2].FinishedAt = rows[2].StartedAt.Add(time.Second)
	rows[2].OutputStatistics = &service.ScheduledTestStatistics{WindowStart: rows[2].StartedAt.Add(-time.Hour), WindowEnd: rows[2].StartedAt,
		RecoveryProbe: &service.ScheduledTestRecoveryProbe{Status: "success", StartedAt: rows[2].StartedAt, FinishedAt: rows[2].FinishedAt}}
	f.complete(rows[2], "pass")
	f.check("quality_paused", false, 10) // The probe cannot skip pending model/candy.
	f.complete(rows[0], "pass")
	f.complete(rows[3], "pass")
	f.review(rows[3], "fail")
	f.complete(rows[1], "pass")
	f.check("quality_paused", false, 10) // A current manual veto remains effective.
	f.review(rows[3], "pass")
	f.check("active", false, 10)
}

func testScheduledTestAllPassPolicyHoldProbe(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, origin int) {
	f := newAllPassFixture(t, ctx, db, plans, repo, true)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows := f.round("allpass-policy-pause", started)
	needed, err := repo.StatisticsRecoveryProbeNeeded(ctx, rows[2])
	require.NoError(t, err)
	require.False(t, needed, "active accounts must not trigger recovery probes")
	f.complete(rows[origin], "fail")
	if origin == 3 {
		f.review(rows[origin], "fail")
	}
	f.check("quality_paused", false, 8, 9)
	var statisticsBlocked bool
	require.NoError(t, db.QueryRowContext(ctx, `SELECT blocked FROM scheduled_test_protection_states WHERE result_id=$1`, rows[2].ID).Scan(&statisticsBlocked))
	require.False(t, statisticsBlocked, "only the policy owns this pause")
	rows = f.round("allpass-policy-recovery", started.Add(time.Minute))
	f.check("quality_paused", false, 8, 9)
	needed, err = repo.StatisticsRecoveryProbeNeeded(ctx, rows[2])
	require.NoError(t, err)
	require.True(t, needed, "a policy-only pause must not deadlock on missing statistics")
	setAllPassStatisticsProbe(rows[2], "success")
	f.complete(rows[2], "pending")
	f.check("quality_paused", false, 8, 9) // The probe cannot skip other checks.
	f.complete(rows[0], "pass")
	f.check("quality_paused", false, 8, 9)
	f.complete(rows[1], "pass")
	f.check("active", false, 10)
	start, err := repo.CacheRecoveryWindowStart(ctx, f.plan.ID, 62, *rows[2].TestDefinitionID)
	require.NoError(t, err)
	require.NotNil(t, start)
	require.Equal(t, rows[2].FinishedAt, *start)
	f.complete(rows[3], "pass")
	f.review(rows[3], "fail")
	f.complete(rows[2], "pass") // Replaying a probe cannot clear a later veto.
	f.check("quality_paused", false, 10)
	_ = f.round("allpass-policy-newer", started.Add(2*time.Minute))
	f.complete(rows[2], "pass") // A previous round cannot release the new hold.
	f.check("quality_paused", false, 10)
	needed, err = repo.StatisticsRecoveryProbeNeeded(ctx, rows[2])
	require.NoError(t, err)
	require.False(t, needed)
}

func setAllPassStatisticsProbe(row *service.ScheduledTestResult, status string) {
	row.FinishedAt = row.StartedAt.Add(time.Second)
	row.OutputStatistics = &service.ScheduledTestStatistics{
		WindowStart: row.StartedAt.Add(-time.Hour), WindowEnd: row.StartedAt,
		RecoveryProbe: &service.ScheduledTestRecoveryProbe{Status: status, StartedAt: row.StartedAt, FinishedAt: row.FinishedAt},
	}
}

func testScheduledTestAllPassPolicyProbeSafeguards(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, scenario string) {
	f := newAllPassFixture(t, ctx, db, plans, repo, scenario != "recovery disabled")
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows := f.round("allpass-guard-pause", started)
	f.complete(rows[0], "fail")
	rows = f.round("allpass-guard-recover", started.Add(time.Minute))
	f.complete(rows[0], "pass")
	modelVerdict := "pass"
	if scenario == "current model failure" {
		modelVerdict = "fail"
	}
	f.complete(rows[1], modelVerdict)
	f.check("quality_paused", false, 8, 9)
	setAllPassStatisticsProbe(rows[2], "success")
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	wantProbe, verdict := false, "pass"
	switch scenario {
	case "failed probe":
		wantProbe, verdict = true, "fail"
		setAllPassStatisticsProbe(rows[2], "failed")
	case "recovery disabled":
		rows[2].OutputStatistics.RecoveryProbe = nil
		verdict = "pending"
	case "manual disable":
		exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
	case "plan disable":
		exec(`UPDATE scheduled_test_plans SET enabled=FALSE WHERE id=$1`, f.plan.ID)
	case "hold released":
		exec(`UPDATE scheduled_test_combination_states SET blocked=FALSE WHERE plan_id=$1`, f.plan.ID)
		exec(`UPDATE accounts SET status='active' WHERE id=62`)
	case "other account hold":
		exec(`UPDATE scheduled_test_combination_states SET account_id=63 WHERE plan_id=$1`, f.plan.ID)
	case "other plan hold":
		other, err := plans.Create(ctx, genericRoutingPlan(*rows[0].TestDefinitionID, 12, 13))
		require.NoError(t, err)
		exec(`UPDATE scheduled_test_combination_states SET plan_id=$1 WHERE plan_id=$2`, other.ID, f.plan.ID)
	case "current manual veto":
		wantProbe = true
		f.complete(rows[3], "pass")
		f.review(rows[3], "fail")
	case "current model failure":
		wantProbe = true
	}
	needed, err := repo.StatisticsRecoveryProbeNeeded(ctx, rows[2])
	require.NoError(t, err)
	require.Equal(t, wantProbe, needed)
	f.complete(rows[2], verdict)
	expectedStatus := "quality_paused"
	if scenario == "hold released" {
		expectedStatus = "active"
	}
	f.check(expectedStatus, false, 8, 9)
	if scenario == "failed probe" {
		var blocked bool
		var phase string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT blocked,recovery_phase FROM scheduled_test_protection_states WHERE result_id=$1`, rows[2].ID).Scan(&blocked, &phase))
		require.True(t, blocked)
		require.Equal(t, "cooldown", phase)
	}
	if scenario == "current manual veto" {
		f.review(rows[3], "pass")
		f.check("active", false, 10)
	}
}

const allPassStatisticsFixture = `CREATE TABLE usage_logs (
 id BIGSERIAL PRIMARY KEY, api_key_id BIGINT DEFAULT 1, request_id TEXT,
 group_id BIGINT DEFAULT 8, account_id BIGINT DEFAULT 62, created_at TIMESTAMPTZ NOT NULL,
 model TEXT DEFAULT 'model', requested_model TEXT, request_type SMALLINT DEFAULT 2,
 stream BOOLEAN DEFAULT true, openai_ws_mode BOOLEAN DEFAULT false, duration_ms INTEGER DEFAULT 0,
 actual_cost NUMERIC DEFAULT 0, total_cost NUMERIC DEFAULT 0,
 input_tokens BIGINT DEFAULT 0, output_tokens BIGINT DEFAULT 0,
 cache_creation_tokens BIGINT DEFAULT 0, cache_read_tokens BIGINT DEFAULT 0,
 image_input_tokens BIGINT DEFAULT 0, image_output_tokens BIGINT DEFAULT 0,
 image_count BIGINT DEFAULT 0, video_count BIGINT DEFAULT 0,
 first_token_ms INTEGER, native_compaction_v2 BOOLEAN DEFAULT false,
 inbound_endpoint TEXT, upstream_endpoint TEXT);
CREATE TABLE ops_error_logs (
 id BIGSERIAL PRIMARY KEY, api_key_id BIGINT DEFAULT 1, request_id TEXT, client_request_id TEXT,
 group_id BIGINT DEFAULT 8, account_id BIGINT DEFAULT 62, created_at TIMESTAMPTZ NOT NULL,
 model TEXT DEFAULT 'model', requested_model TEXT, request_type SMALLINT DEFAULT 2, duration_ms INTEGER DEFAULT 0,
 status_code INTEGER DEFAULT 502, error_type TEXT DEFAULT 'upstream_error', is_count_tokens BOOLEAN DEFAULT false);`
