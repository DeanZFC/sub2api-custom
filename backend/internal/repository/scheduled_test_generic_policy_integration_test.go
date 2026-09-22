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

func genericRoutingPlan(definitionID int64, groups ...int64) *service.ScheduledTestPlan {
	return &service.ScheduledTestPlan{Name: "Generic strategy", GroupIDs: groups, GroupID: &groups[0], TargetMode: "all_accounts", TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{definitionID}, ModelID: "model", Enabled: true, MaxResults: 20, CronExpression: "0 * * * *", Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{{TestDefinitionID: definitionID, ExpectedAnswer: "21", AnswerMatch: "numeric", OnPass: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{groups[len(groups)-1]}}, OnFail: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{groups[0]}}}}}}
}

func genericRoundInputs(plan *service.ScheduledTestPlan, accounts []int64, started time.Time) []*service.ScheduledTestResult {
	var inputs []*service.ScheduledTestResult
	for _, accountID := range accounts {
		for _, definitionID := range plan.TestDefinitionIDs {
			accountID, definitionID := accountID, definitionID
			inputs = append(inputs, &service.ScheduledTestResult{PlanID: plan.ID, GroupID: plan.GroupID, AccountID: &accountID, TestDefinitionID: &definitionID, TargetMode: plan.TargetMode, ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, Status: "pending", OutputKind: "number", StartedAt: started, FinishedAt: started})
		}
	}
	return inputs
}

func testScheduledTestGenericPolicy(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	var modelID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='model_check'`).Scan(&modelID))
	plan := genericRoutingPlan(candyID, 8, 10)
	plan.GroupIDs = append(plan.GroupIDs, 11)
	plan.TestDefinitionIDs = []int64{candyID, modelID, pelicanID}
	plan.Protection.Rules = append(plan.Protection.Rules,
		service.ScheduledTestProtectionRule{TestDefinitionID: modelID, ModelMatch: "exact", RequiredPass: true, Priority: 20},
		service.ScheduledTestProtectionRule{TestDefinitionID: pelicanID, Priority: 100, Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}, OnPass: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{10}}, OnFail: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{8}}})
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	exec(`INSERT INTO account_groups(account_id,group_id) VALUES(62,10)`)
	targets, err := repo.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63}, targets, "an account present in both source groups is frozen once")
	started := time.Now().UTC().Truncate(time.Microsecond)
	rows, err := repo.BeginRun(ctx, plan, "generic-first", genericRoundInputs(plan, targets, started))
	require.NoError(t, err)
	require.Len(t, rows, 6)
	exec(`INSERT INTO accounts(id,name) VALUES(64,'Joined during run');INSERT INTO account_groups(account_id,group_id) VALUES(64,10)`)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started))
	var enrolled int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=64`, plan.ID).Scan(&enrolled))
	require.Zero(t, enrolled, "round initialization only uses the frozen snapshot")
	complete := func(index int, verdict string) {
		t.Helper()
		row := rows[index]
		require.NoError(t, repo.BeginProtection(ctx, row, plan.Protection.Rules[index%3]))
		row.Status = "success"
		if index%3 == 2 {
			row.OutputKind = "html"
			row.OutputHTML = "<svg/>"
		}
		require.NoError(t, repo.Update(ctx, row))
		require.NoError(t, repo.CompleteProtection(ctx, row, verdict, ""))
	}
	bindings := func() []int64 {
		t.Helper()
		var ids pq.Int64Array
		require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&ids))
		return ids
	}
	complete(0, "pass")
	require.Equal(t, []int64{8, 9, 10}, bindings(), "required check blocks premature promotion")
	complete(2, "pass")
	reviews, err := repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.NoError(t, repo.DecideTestResult(ctx, 1, rows[2].ID, reviews[0].Generation, "pass"))
	require.Equal(t, []int64{8, 9, 10}, bindings(), "administrator cannot bypass required pending evidence")
	complete(1, "fail")
	require.Equal(t, []int64{8, 9, 10}, bindings(), "required failure without group action only blocks promotion")
	require.NoError(t, repo.DecideTestResult(ctx, 1, rows[2].ID, reviews[0].Generation, "fail"))
	require.Equal(t, []int64{8}, bindings(), "required failure permits a manual downgrade and removes every unrelated group")
	require.NoError(t, repo.DecideTestResult(ctx, 1, rows[2].ID, reviews[0].Generation, "pass"))
	require.Equal(t, []int64{8}, bindings())
	complete(1, "pass")
	require.Equal(t, []int64{10}, bindings(), "required pass releases the current administrator decision")
	eligible, err := repo.IsPlanRunAccountEligible(ctx, plan.ID, "generic-first", 62)
	require.NoError(t, err)
	require.True(t, eligible, "membership movement does not skip later tests")
	eligible, err = repo.IsPlanRunAccountEligible(ctx, plan.ID, "generic-first", 64)
	require.NoError(t, err)
	require.False(t, eligible, "late arrivals cannot join an existing round")
	exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
	eligible, err = repo.IsPlanRunAccountEligible(ctx, plan.ID, "generic-first", 62)
	require.NoError(t, err)
	require.False(t, eligible, "manual scheduling stop always wins")
	exec(`UPDATE accounts SET schedulable=TRUE WHERE id=62`)
	// Pure display reorder changes neither run ownership nor review generation.
	plan.GroupIDs = []int64{10, 8, 11}
	plan.GroupID = &plan.GroupIDs[0]
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	reviews, err = repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, "pass", reviews[0].AdminVerdict)
	require.NoError(t, repo.DecideTestResult(ctx, 1, rows[2].ID, reviews[0].Generation, "fail"))
	require.Equal(t, []int64{8}, bindings())
	// Every subsequent round freezes the union again and clears old judgments.
	targets, err = repo.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63, 64}, targets)
	_, err = repo.BeginRun(ctx, plan, "generic-second", genericRoundInputs(plan, targets, started.Add(time.Hour)))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started.Add(time.Hour)))
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, rows[2].ID, reviews[0].Generation, "pass"), service.ErrScheduledTestVoteUnavailable)
	eligible, err = repo.IsPlanRunAccountEligible(ctx, plan.ID, "generic-first", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	// Removing a secondary source invalidates the round even when its anchor,
	// tests and protection rules are unchanged.
	staleScope := *plan
	plan.GroupIDs = []int64{10, 8}
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	eligible, err = repo.IsPlanRunAccountEligible(ctx, plan.ID, "generic-second", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	require.ErrorContains(t, repo.BeginProtectionRun(ctx, &staleScope, started.Add(2*time.Hour)), "changed before execution")
	_, err = repo.BeginRun(ctx, &staleScope, "stale-scope", genericRoundInputs(&staleScope, []int64{62}, started.Add(2*time.Hour)))
	require.ErrorContains(t, err, "changed before execution")
	// A model edit likewise cannot publish an obsolete snapshot.
	stale := *plan
	plan.ModelID = "new-model"
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	_, err = repo.BeginRun(ctx, &stale, "stale", genericRoundInputs(&stale, []int64{62}, started.Add(2*time.Hour)))
	require.ErrorContains(t, err, "changed before execution")
	eligible, err = repo.IsPlanRunAccountEligible(ctx, plan.ID, "generic-second", 62)
	require.NoError(t, err)
	require.False(t, eligible)
	var latest string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT latest_run_id FROM scheduled_test_plans WHERE id=$1`, plan.ID).Scan(&latest))
	require.Empty(t, latest)
	// Disabling only stops the scheduler and protection, while manual detection works.
	plan.Enabled = false
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	_, err = repo.BeginRun(ctx, plan, "manual-disabled", genericRoundInputs(plan, []int64{62}, started.Add(3*time.Hour)))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, started.Add(3*time.Hour)))
	eligible, err = repo.IsPlanRunAccountEligible(ctx, plan.ID, "manual-disabled", 62)
	require.NoError(t, err)
	require.True(t, eligible)
}

func testScheduledTestGenericConflicts(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, definitionID int64) {
	t.Helper()
	repo := &scheduledTestResultRepository{db: db}
	first, err := plans.Create(ctx, genericRoutingPlan(definitionID, 8, 10))
	require.NoError(t, err)
	_, err = plans.Create(ctx, genericRoutingPlan(definitionID, 10, 11))
	require.ErrorContains(t, err, "相同分组或账号")
	_, err = plans.Create(ctx, genericRoutingPlan(definitionID, 9, 12))
	require.ErrorContains(t, err, "相同分组或账号", "shared account in distinct groups is also rejected")
	second, err := plans.Create(ctx, genericRoutingPlan(definitionID, 12, 13))
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	frozen, err := repo.BeginRun(ctx, first, "before-conflict", genericRoundInputs(first, []int64{62}, started))
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, first, started))
	require.NoError(t, repo.BeginProtection(ctx, frozen[0], first.Protection.Rules[0]))
	_, err = db.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id) VALUES(62,12)`)
	require.NoError(t, err)
	for i, plan := range []*service.ScheduledTestPlan{first, second} {
		_, err = repo.BeginRun(ctx, plan, fmt.Sprintf("conflict-%d", i), genericRoundInputs(plan, []int64{62}, started.Add(time.Hour)))
		require.ErrorContains(t, err, "相同分组或账号", "post-configuration account edits cannot enable duplicate strategies")
	}
	eligible, err := repo.IsPlanRunAccountEligible(ctx, first.ID, "before-conflict", 62)
	require.NoError(t, err)
	require.False(t, eligible, "already queued attempts also recheck ownership")
	err = repo.CompleteProtection(ctx, frozen[0], "pass", "")
	require.ErrorContains(t, err, "conflicting enabled test strategies", "an old in-flight response cannot move a newly conflicted account")
	var ids pq.Int64Array
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&ids))
	require.Equal(t, []int64{8, 9, 12}, []int64(ids))
}
