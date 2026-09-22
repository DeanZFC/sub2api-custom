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

func combinedTestLeaf(id int64, verdict string) service.ScheduledTestCombinationCondition {
	return service.ScheduledTestCombinationCondition{Operator: "test", TestDefinitionID: id, Verdict: verdict}
}

func combinedRoutingPlan(candyID, pelicanID int64) *service.ScheduledTestPlan {
	p := genericRoutingPlan(candyID, 8, 10, 11)
	p.Protection.Mode = "combined"
	p.TestDefinitionIDs = []int64{candyID, pelicanID}
	// These deliberately conflicting legacy actions must remain inactive in
	// combined mode, including the old required-pass and priority controls.
	p.Protection.Rules[0].Priority = 1000
	p.Protection.Rules[0].OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{13}}
	p.Protection.Rules = append(p.Protection.Rules, service.ScheduledTestProtectionRule{
		TestDefinitionID: pelicanID, RequiredPass: true, PauseOnFailure: true,
		OnFail: &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{13}},
	})
	p.Protection.Combinations = []service.ScheduledTestCombinationRule{
		{ID: "both-pass", Name: "Both checks pass", Priority: 1,
			Condition: service.ScheduledTestCombinationCondition{Operator: "all", Conditions: []service.ScheduledTestCombinationCondition{combinedTestLeaf(candyID, "pass"), combinedTestLeaf(pelicanID, "pass")}},
			Action:    service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{10}}},
		{ID: "any-fail", Name: "Either check fails", Priority: 1,
			Condition: service.ScheduledTestCombinationCondition{Operator: "any", Conditions: []service.ScheduledTestCombinationCondition{combinedTestLeaf(candyID, "fail"), combinedTestLeaf(pelicanID, "fail")}},
			Action:    service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{8}}},
	}
	return p
}

func testScheduledTestCombinedLifecycle(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	plan, err := plans.Create(ctx, combinedRoutingPlan(candyID, pelicanID))
	require.NoError(t, err, "unused legacy group destinations are not validated in combined mode")
	started := time.Now().UTC().Truncate(time.Microsecond)
	newRound := func(runID string, at time.Time) []*service.ScheduledTestResult {
		t.Helper()
		rows, err := repo.BeginRun(ctx, plan, runID, genericRoundInputs(plan, []int64{62}, at))
		require.NoError(t, err)
		require.NoError(t, repo.BeginProtectionRun(ctx, plan, at))
		for i, row := range rows {
			require.NoError(t, repo.BeginProtection(ctx, row, plan.Protection.Rules[i]))
		}
		return rows
	}
	complete := func(row *service.ScheduledTestResult, verdict string) {
		t.Helper()
		row.Status = "success"
		if verdict == "fail" {
			row.Status = "failed"
		}
		require.NoError(t, repo.Update(ctx, row))
		require.NoError(t, repo.CompleteProtection(ctx, row, verdict, ""))
	}
	check := func(status string, groups ...int64) {
		t.Helper()
		var got string
		var memberships pq.Int64Array
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&got))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&memberships))
		require.Equal(t, status, got)
		require.Equal(t, groups, []int64(memberships))
	}
	first := newRound("combination-first", started)
	complete(first[0], "pass")
	check("active", 8, 9)
	complete(first[1], "pass")
	check("active", 10)
	second := newRound("combination-second", started.Add(time.Minute))
	check("active", 10)
	complete(second[0], "fail")
	check("quality_paused", 8)
	complete(second[1], "pass")
	check("quality_paused", 8)
	staleAttempt := *second[0]
	second[0].StartedAt = started.Add(2 * time.Minute)
	second[0].Status = "running"
	require.NoError(t, repo.RestartFailed(ctx, second[0]))
	require.NoError(t, repo.BeginProtection(ctx, second[0], plan.Protection.Rules[0]))
	complete(second[1], "pass")
	check("quality_paused", 8) // Pending retry preserves the previous hold.
	complete(second[0], "pass")
	check("active", 10)
	require.NoError(t, repo.CompleteProtection(ctx, &staleAttempt, "fail", "late failure"))
	check("active", 10)
	third := newRound("combination-third", started.Add(3*time.Minute))
	complete(third[0], "fail")
	check("quality_paused", 8)
	require.NoError(t, repo.CompleteProtection(ctx, second[0], "pass", "old round"))
	check("quality_paused", 8)
	require.NoError(t, repo.Delete(ctx, third[0].ID))
	complete(third[1], "pass")
	check("quality_paused", 8) // Deleting evidence never clears a hold.
	plan.Protection.Combinations[0].Name = "Edited policy"
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	check("active", 8) // Editing clears this strategy's scheduling hold only.
	var holds int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_combination_states WHERE plan_id=$1`, plan.ID).Scan(&holds))
	require.Zero(t, holds)
	require.NoError(t, repo.CompleteProtection(ctx, third[1], "pass", "obsolete config"))
	check("active", 8)
	fourth := newRound("combination-fourth", started.Add(4*time.Minute))
	complete(fourth[0], "fail")
	check("quality_paused", 8)
	newRound("combination-fifth", started.Add(5*time.Minute))
	check("quality_paused", 8) // A new hourly round does not release a pause.
	require.NoError(t, plans.Delete(ctx, plan.ID))
	check("active", 8)
}

func testScheduledTestCombinedVotes(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	plan := combinedRoutingPlan(candyID, pelicanID)
	plan.Protection.Rules[1].RequiredPass = false
	plan.Protection.Rules[1].Vote = &service.ScheduledTestVoteConfig{Enabled: true, PublicEnabled: true, PassAtLeast: 2, RejectAbove: 1}
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := repo.BeginRun(ctx, plan, "combined-votes", genericRoundInputs(plan, []int64{62}, started))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started))
	for i, row := range rows {
		require.NoError(t, repo.BeginProtection(ctx, row, plan.Protection.Rules[i]))
		row.Status, row.OutputKind, row.OutputHTML = "success", "html", "<svg/>"
		require.NoError(t, repo.Update(ctx, row))
		require.NoError(t, repo.CompleteProtection(ctx, row, "pass", ""))
	}
	group := func(want int64) {
		t.Helper()
		var got int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT MIN(group_id) FROM account_groups WHERE account_id=62`).Scan(&got))
		require.Equal(t, want, got)
	}
	group(8)
	_, err = repo.CastTestVote(ctx, 1, rows[1].ID, "pass")
	require.NoError(t, err)
	group(8)
	_, err = repo.CastTestVote(ctx, 2, rows[1].ID, "pass")
	require.NoError(t, err)
	group(10)
	reviews, err := repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	var generation int64
	for _, review := range reviews {
		if review.Result.ID == rows[1].ID {
			generation = review.Generation
		}
	}
	require.NotZero(t, generation)
	require.NoError(t, repo.DecideTestResult(ctx, 1, rows[1].ID, generation, "fail"))
	group(8)
	require.NoError(t, repo.DecideTestResult(ctx, 1, rows[1].ID, generation, "pass"))
	group(10)
	_, err = repo.BeginRun(ctx, plan, "combined-votes-next", genericRoundInputs(plan, []int64{62}, started.Add(time.Hour)))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started.Add(time.Hour)))
	_, err = repo.CastTestVote(ctx, 3, rows[1].ID, "fail")
	require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, rows[1].ID, generation, "fail"), service.ErrScheduledTestVoteUnavailable)
	group(10)
}

func testScheduledTestCombinedConflictAndCleanup(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	plan, err := plans.Create(ctx, combinedRoutingPlan(candyID, pelicanID))
	require.NoError(t, err)
	_, err = plans.Create(ctx, genericRoutingPlan(candyID, 10, 11))
	require.ErrorContains(t, err, "相同分组或账号")
	other, err := plans.Create(ctx, genericRoutingPlan(candyID, 12, 13))
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := repo.BeginRun(ctx, plan, "combination-ownership", genericRoundInputs(plan, []int64{62}, started))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started))
	require.NoError(t, repo.BeginProtection(ctx, rows[0], plan.Protection.Rules[0]))
	_, err = db.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES(62,12)`)
	require.NoError(t, err)
	eligible, err := repo.IsPlanRunAccountEligible(ctx, plan.ID, "combination-ownership", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	require.ErrorContains(t, repo.CompleteProtection(ctx, rows[0], "fail", ""), "conflicting enabled test strategies")
	_, err = db.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=62 AND group_id=12`)
	require.NoError(t, err)
	require.NoError(t, repo.CompleteProtection(ctx, rows[0], "fail", ""))
	// Simulate a separately owned safety hold, then disable the combination
	// strategy. Its cleanup must not enable traffic through the other hold.
	_, err = db.ExecContext(ctx, `INSERT INTO scheduled_test_combination_states(plan_id,account_id,blocked,reason) VALUES($1,62,TRUE,'other strategy')`, other.ID)
	require.NoError(t, err)
	plan.Enabled = false
	_, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "quality_paused", status)
	require.NoError(t, plans.Delete(ctx, other.ID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "active", status)
}

func testScheduledTestCombinedMergedActions(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	plan := combinedRoutingPlan(candyID, pelicanID)
	plan.Protection.Combinations = []service.ScheduledTestCombinationRule{
		{ID: "resume-and-group", Priority: 4, Condition: combinedTestLeaf(candyID, "pass"), Action: service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{10}}},
		{ID: "pause-and-group", Priority: 4, Condition: combinedTestLeaf(candyID, "pass"), Action: service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{11}}},
		{ID: "lower", Priority: 1, Condition: combinedTestLeaf(candyID, "pass"), Action: service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{8}}},
	}
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := repo.BeginRun(ctx, plan, "combined-merge", genericRoundInputs(plan, []int64{62}, started))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started))
	require.NoError(t, repo.BeginProtection(ctx, rows[0], plan.Protection.Rules[0]))
	require.NoError(t, repo.CompleteProtection(ctx, rows[0], "pass", ""))
	var groups pq.Int64Array
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&groups))
	require.Equal(t, []int64{10, 11}, []int64(groups))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "quality_paused", status)
	var reason string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT protection_decision->>'reason' FROM scheduled_test_results WHERE id=$1`, rows[0].ID).Scan(&reason))
	require.Contains(t, reason, "resume-and-group")
	require.Contains(t, reason, "pause-and-group")
	require.NotContains(t, reason, "lower")
}
