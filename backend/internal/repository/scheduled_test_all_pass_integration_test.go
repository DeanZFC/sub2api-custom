//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
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

func testScheduledTestAllPassConsecutiveRounds(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository) {
	f := newAllPassFixture(t, ctx, db, plans, repo, false)
	f.plan.Protection.RequiredConsecutivePasses = 2
	_, err := plans.Update(ctx, f.plan)
	require.NoError(t, err)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	progress := func(row *service.ScheduledTestResult, count int, verdict string) {
		t.Helper()
		require.NoError(t, repo.hydrateAllPassProgress(ctx, []*service.ScheduledTestResult{row}))
		require.NotNil(t, row.AllPassProgress)
		require.Equal(t, 2, row.AllPassProgress.Required)
		require.Equal(t, count, row.AllPassProgress.ConsecutivePasses)
		require.Equal(t, verdict, row.AllPassProgress.RoundVerdict)
	}
	complete := func(rows []*service.ScheduledTestResult) {
		for i := 0; i < 3; i++ {
			f.complete(rows[i], "pass")
		}
	}
	failed := f.round("consecutive-fail", at)
	f.complete(failed[0], "fail")
	f.check("quality_paused", false, 8, 9)
	progress(failed[0], 0, "fail")
	complete(failed)
	f.complete(failed[3], "pass")
	progress(failed[0], 0, "fail")
	f.check("quality_paused", false, 8, 9)
	first := f.round("consecutive-first", at.Add(time.Minute))
	complete(first)
	f.check("quality_paused", false, 8, 9)
	progress(first[0], 1, "pass")
	// Completion/review reentry cannot manufacture another successful round.
	f.complete(first[0], "pass")
	f.complete(first[3], "pass")
	f.review(first[3], "pass")
	f.review(first[3], "pass")
	progress(first[0], 1, "pass")
	f.check("quality_paused", false, 8, 9)
	second := f.round("consecutive-second", at.Add(2*time.Minute))
	progress(second[0], 1, "pending")
	complete(second)
	progress(second[0], 2, "pass")
	f.check("active", false, 10)
	f.complete(second[3], "pass")
	f.review(second[3], "fail")
	progress(second[0], 0, "fail")
	f.check("quality_paused", false, 10)
	f.review(second[3], "pass")
	progress(second[0], 2, "pass")
	f.check("active", false, 10)
	// Altering the question cannot consume successes from the previous prompt.
	var originalPrompt string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT prompt FROM scheduled_test_definitions WHERE id=$1`, f.plan.Protection.Rules[0].TestDefinitionID).Scan(&originalPrompt))
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_definitions SET prompt=prompt || ' changed' WHERE id=$1`, f.plan.Protection.Rules[0].TestDefinitionID)
	require.NoError(t, err)
	changed := f.round("consecutive-changed-prompt", at.Add(150*time.Second))
	complete(changed)
	progress(changed[0], 1, "pass")
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_definitions SET prompt=$2 WHERE id=$1`, f.plan.Protection.Rules[0].TestDefinitionID, originalPrompt)
	require.NoError(t, err)
	reverted := f.round("consecutive-reverted-prompt", at.Add(160*time.Second))
	complete(reverted)
	progress(reverted[0], 1, "pass")
	// An unresolved abandoned round is a real break, even if later rounds pass.
	abandoned := f.round("consecutive-abandoned", at.Add(3*time.Minute))
	f.complete(abandoned[0], "pass")
	third := f.round("consecutive-after-pending", at.Add(4*time.Minute))
	complete(third)
	progress(third[0], 1, "pass")
	// A later explicit failure always pauses immediately and resets progress.
	f.complete(third[3], "pass")
	f.review(third[3], "fail")
	progress(third[0], 0, "fail")
	f.check("quality_paused", false, 10)
	// Editing the required count invalidates both stale approval and streaks.
	f.plan.Protection.RequiredConsecutivePasses = 3
	_, err = plans.Update(ctx, f.plan)
	require.NoError(t, err)
	var saved int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_all_pass_rounds WHERE plan_id=$1`, f.plan.ID).Scan(&saved))
	require.Zero(t, saved)
	var generation int64
	_ = db.QueryRowContext(ctx, `SELECT generation FROM scheduled_test_protection_states WHERE result_id=$1`, third[3].ID).Scan(&generation)
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, third[3].ID, generation, "pass"), service.ErrScheduledTestVoteUnavailable)
	// Retention is bounded independently of whether individual results survive.
	for i := 0; i < 105; i++ {
		_, err = db.ExecContext(ctx, `INSERT INTO scheduled_test_all_pass_rounds(plan_id,account_id,model_key,run_id,started_at,rule_version,verdict) VALUES($1,62,$2,$3,$4,'old','pass')`, f.plan.ID, f.plan.ModelID, fmt.Sprintf("retention-%d", i), at.Add(time.Duration(i-200)*time.Minute))
		require.NoError(t, err)
	}
	_ = f.round("retention-current", at.Add(5*time.Minute))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_all_pass_rounds WHERE plan_id=$1`, f.plan.ID).Scan(&saved))
	require.Equal(t, 101, saved)
}

func testScheduledTestPelicanDuration(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository) {
	f := newAllPassFixture(t, ctx, db, plans, repo, false)
	f.plan.Protection.Rules[3].Thresholds = []service.ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 60000}}
	_, err := plans.Update(ctx, f.plan)
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows := f.round("duration-slow", started)
	for i := 0; i < 3; i++ {
		f.complete(rows[i], "pass")
	}
	rows[3].LatencyMs = 60001
	f.complete(rows[3], "fail")
	f.check("quality_paused", false, 8, 9)
	stored, err := repo.GetByID(ctx, rows[3].ID)
	require.NoError(t, err)
	require.EqualValues(t, 60001, stored.LatencyMs)
	require.Equal(t, "<svg/>", stored.OutputHTML)
	f.review(rows[3], "pass")
	f.check("quality_paused", false, 8, 9)
	var verdict, reason string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT verdict,reason FROM scheduled_test_protection_states WHERE result_id=$1`, rows[3].ID).Scan(&verdict, &reason))
	require.Equal(t, "fail", verdict)
	require.Contains(t, reason, "60001")
	next := f.round("duration-healthy", started.Add(time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(next[i], "pass")
	}
	f.check("quality_paused", false, 8, 9)
	next[3].LatencyMs = 60000
	f.complete(next[3], "pass")
	f.check("active", false, 10)
	f.review(next[3], "fail")
	f.check("quality_paused", false, 10)
	f.review(next[3], "pass")
	f.check("active", false, 10)

	// A fast upstream error did not complete generation within the limit.
	// It must not satisfy the duration gate or recover via an admin approval.
	failed := f.round("duration-failed", started.Add(2*time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(failed[i], "pass")
	}
	failed[3].Status, failed[3].LatencyMs = "failed", 100
	require.NoError(t, repo.Update(ctx, failed[3]))
	require.NoError(t, repo.CompleteProtection(ctx, failed[3], "fail", "execution failed"))
	f.check("quality_paused", false, 10)
	var generation int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT generation FROM scheduled_test_protection_states WHERE result_id=$1`, failed[3].ID).Scan(&generation))
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, failed[3].ID, generation, "pass"), service.ErrScheduledTestVoteUnavailable)
	f.check("quality_paused", false, 10)
}

func newAllPassFixture(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, recovery bool, thresholds ...service.ScheduledTestThreshold) allPassFixture {
	t.Helper()
	ids := make([]int64, 4)
	_, err := db.ExecContext(ctx, `INSERT INTO scheduled_test_definitions(key,name,prompt,output_kind,enabled) VALUES('active_secondary','Active secondary','Reply PASS','text',true) ON CONFLICT(key) DO NOTHING`)
	require.NoError(t, err)
	for i, key := range []string{"candy", "model_check", "active_secondary", "pelican"} {
		require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key=$1`, key).Scan(&ids[i]))
	}
	plan := genericRoutingPlan(ids[0], 8, 10)
	plan.Protection.Mode, plan.Protection.PassGroupIDs = "all_pass", []int64{10}
	plan.TestDefinitionIDs = ids
	plan.Protection.Rules = append(plan.Protection.Rules,
		service.ScheduledTestProtectionRule{TestDefinitionID: ids[1], ModelMatch: "exact"},
		service.ScheduledTestProtectionRule{TestDefinitionID: ids[2], ExpectedAnswer: "PASS"},
		service.ScheduledTestProtectionRule{TestDefinitionID: ids[3], Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}},
	)
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
	plan, err = plans.Create(ctx, plan)
	require.NoError(t, err)
	f := allPassFixture{plan: plan}
	f.round = func(run string, at time.Time) []*service.ScheduledTestResult {
		t.Helper()
		inputs := genericRoundInputs(plan, []int64{62}, at)
		for i, row := range inputs {
			row.OutputKind = []string{"number", "model_check", "text", "html"}[i]
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

func testScheduledTestAllPassAutomaticReview(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository) {
	t.Helper()
	_, err := db.ExecContext(ctx, `UPDATE quality_operations_config SET config=jsonb_set(config,'{pelican_review}', '{"enabled":true,"judge_account_id":63,"judge_model":"vision","daily_requests":100}'::jsonb)`)
	require.NoError(t, err)
	f := newAllPassFixture(t, ctx, db, plans, repo, false)
	f.plan.Protection.Rules[3].AutoReview = &service.ScheduledTestAutoReviewConfig{Enabled: true}
	updated, err := plans.Update(ctx, f.plan)
	require.NoError(t, err)
	f.plan = updated
	complete := func(row *service.ScheduledTestResult, verdict string) {
		t.Helper()
		row.ExecutionInfo = &service.ScheduledTestExecutionInfo{AutoReview: &service.ScheduledTestAutoReviewSummary{Verdict: verdict, JudgeAccountID: 63, JudgeModel: "vision", RubricVersion: service.ScheduledTestReviewRubricVersion}}
		f.complete(row, verdict)
	}
	started := time.Now().UTC().Truncate(time.Microsecond)
	held := f.round("auto-review-pending-first", started)
	f.complete(held[0], "pass")
	f.complete(held[1], "pass")
	f.complete(held[2], "fail")
	complete(held[3], "pending")
	f.check("quality_paused", false, 8, 9)
	pending := f.round("auto-review-pending-next", started.Add(time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(pending[i], "pass")
	}
	complete(pending[3], "pending")
	f.check("quality_paused", false, 8, 9)
	passed := f.round("auto-review-pass", started.Add(2*time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(passed[i], "pass")
	}
	complete(passed[3], "pass")
	f.check("active", false, 10)
	failed := f.round("auto-review-fail", started.Add(3*time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(failed[i], "pass")
	}
	complete(failed[3], "fail")
	f.check("quality_paused", false, 10)
	f.review(failed[3], "pass")
	f.check("active", false, 10)
	overridden := f.round("auto-review-admin-veto", started.Add(4*time.Minute))
	for i := 0; i < 3; i++ {
		f.complete(overridden[i], "pass")
	}
	complete(overridden[3], "pass")
	f.review(overridden[3], "fail")
	f.check("quality_paused", false, 10)
}
