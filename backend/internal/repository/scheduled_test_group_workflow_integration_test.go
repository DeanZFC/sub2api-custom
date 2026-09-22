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

// Reuse the actions fixture so the workflow exercises the real account/group,
// protection, review, action audit and scheduler-outbox constraints together.
func testScheduledTestGroupWorkflow(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	groupID, accountID := int64(8), int64(62)
	workflow := &service.ScheduledTestGroupWorkflow{AutomaticTestID: candyID, ReviewTestID: pelicanID, PassGroupID: 10, FailGroupID: 8}
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Direct group workflow", GroupID: &groupID, TargetMode: "all_accounts",
		TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID, pelicanID},
		ModelID: "model", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20,
		Protection: service.ScheduledTestProtectionConfig{Enabled: true, GroupWorkflow: workflow, Rules: workflow.Rules()},
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=63;
 INSERT INTO account_groups(account_id,group_id) VALUES(63,10)`)
	require.NoError(t, err)
	targets, err := repo.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63}, targets, "new accounts in either tier participate")
	detected, err := repo.ListPlanDetectionAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, targets, detected)

	bindings := func() []int64 {
		t.Helper()
		var ids pq.Int64Array
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(array_agg(group_id ORDER BY group_id),'{}'::bigint[]) FROM account_groups WHERE account_id=$1`, accountID).Scan(&ids))
		return []int64(ids)
	}
	round := time.Now().UTC().Truncate(time.Microsecond)
	startRound := func() {
		t.Helper()
		round = round.Add(time.Hour)
		require.NoError(t, repo.BeginProtectionRun(ctx, plan, round))
	}
	begin := func(index int) *service.ScheduledTestResult {
		t.Helper()
		rule := plan.Protection.Rules[index]
		kind := "number"
		if index == 1 {
			kind = "html"
		}
		result, err := repo.Create(ctx, &service.ScheduledTestResult{
			PlanID: plan.ID, TestDefinitionID: &rule.TestDefinitionID, GroupID: plan.GroupID,
			AccountID: &accountID, TargetMode: plan.TargetMode, ModelID: plan.ModelID,
			Status: "running", OutputKind: kind, StartedAt: round, FinishedAt: round,
		})
		require.NoError(t, err)
		require.NoError(t, repo.BeginProtection(ctx, result, rule))
		return result
	}
	complete := func(result *service.ScheduledTestResult, verdict string) {
		t.Helper()
		result.Status, result.ResponseText = "success", "21"
		if result.OutputKind == "html" {
			result.OutputHTML = "<svg>animation</svg>"
		}
		require.NoError(t, repo.Update(ctx, result))
		require.NoError(t, repo.CompleteProtection(ctx, result, verdict, "numeric result differed"))
	}
	review := func() *service.ScheduledTestAdminReview {
		t.Helper()
		rows, err := repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Len(t, rows, 1)
		require.True(t, rows[0].GroupWorkflow)
		return rows[0]
	}

	startRound()
	automatic := begin(0)
	complete(automatic, "pass")
	require.Equal(t, []int64{10}, bindings(), "numeric pass immediately replaces all memberships, including unrelated group 9")
	var audited []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT protection_decision FROM scheduled_test_results WHERE id=$1`, automatic.ID).Scan(&audited))
	require.Contains(t, string(audited), `"removed_group_ids": [8, 9]`)
	var events int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=$1 AND event_type=$2`, accountID, service.SchedulerOutboxEventAccountGroupsChanged).Scan(&events))
	require.Positive(t, events)
	manual := begin(1)
	complete(manual, "pass")
	require.Equal(t, []int64{10}, bindings(), "generating an animation does not move the account")
	current := review()
	require.Equal(t, int64(10), *current.Result.GroupID, "review follows actual tier rather than original plan group")
	require.NoError(t, repo.DecideTestResult(ctx, 1, manual.ID, current.Generation, "fail"))
	require.Equal(t, []int64{8}, bindings())
	require.Equal(t, int64(8), *review().Result.GroupID)
	require.NoError(t, repo.DecideTestResult(ctx, 1, manual.ID, current.Generation, "pass"))
	require.Equal(t, []int64{10}, bindings())
	public, err := repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, public, "workflow review is administrator-only")
	_, err = repo.CastTestVote(ctx, 1, manual.ID, "fail")
	require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)

	startRound()
	require.Equal(t, []int64{10}, bindings(), "new round keeps placement until fresh automatic result")
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, manual.ID, current.Generation, "fail"), service.ErrScheduledTestVoteUnavailable)
	rows, err := repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Empty(t, rows, "old review disappears immediately at round start")
	automatic = begin(0)
	complete(automatic, "fail")
	require.Equal(t, []int64{8}, bindings(), "fresh automatic failure resets last hour's administrator pass")
	manual = begin(1)
	complete(manual, "pass")
	require.Equal(t, []int64{8}, bindings(), "unreviewed animation cannot promote failed numeric result")
	current = review()
	require.Empty(t, current.AdminVerdict)
	require.NoError(t, repo.DecideTestResult(ctx, 1, manual.ID, current.Generation, "pass"))
	require.Equal(t, []int64{10}, bindings(), "administrator pass overrides the same-round numeric failure")
	require.NoError(t, repo.CompleteProtection(ctx, automatic, "fail", "late duplicate completion"))
	require.Equal(t, []int64{10}, bindings(), "late numeric completion cannot erase same-round manual placement")
	require.NoError(t, repo.DecideTestResult(ctx, 1, manual.ID, current.Generation, "fail"))
	require.Equal(t, []int64{8}, bindings())

	_, err = db.ExecContext(ctx, `UPDATE accounts SET schedulable=FALSE WHERE id=$1`, accountID)
	require.NoError(t, err)
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, manual.ID, current.Generation, "pass"), service.ErrScheduledTestVoteUnavailable)
	detected, err = repo.ListPlanDetectionAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{63}, detected)
	targets, err = repo.ListPlanTargetAccountIDs(ctx, plan, nil)
	require.NoError(t, err)
	require.Equal(t, []int64{62, 63}, targets, "manual stop remains visible as a skipped target")
	_, err = db.ExecContext(ctx, `UPDATE accounts SET schedulable=TRUE WHERE id=$1`, accountID)
	require.NoError(t, err)
	startRound()
	automatic = begin(0)
	complete(automatic, "pass")
	require.Equal(t, []int64{10}, bindings(), "next round's numeric pass discards previous manual failure")
	manual = begin(1)
	manual.Status, manual.ErrorMessage = "failed", "upstream timeout"
	require.NoError(t, repo.Update(ctx, manual))
	require.NoError(t, repo.CompleteProtection(ctx, manual, "fail", manual.ErrorMessage))
	require.Equal(t, []int64{10}, bindings(), "animation execution failure records failure without changing placement")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT protection_decision FROM scheduled_test_results WHERE id=$1`, manual.ID).Scan(&audited))
	require.Contains(t, string(audited), "动画执行失败，保留当前分组：upstream timeout")
	require.Contains(t, string(audited), `"verdict": "fail"`)
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=$1`, accountID).Scan(&status))
	require.Equal(t, "active", status)

	// Simulate an older queued plan that predates workflow conflict validation.
	// Its protection hold must still apply, but it cannot restore old groups.
	legacyGroupID := int64(10)
	legacyRule := service.ScheduledTestProtectionRule{
		TestDefinitionID: candyID, PauseOnFailure: true,
		OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{10}},
		OnFail: &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{11}},
	}
	legacy, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Queued legacy plan", GroupID: &legacyGroupID, TargetMode: "all_accounts",
		TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID}, ModelID: "model",
		CronExpression: "0 * * * *", Enabled: false, MaxResults: 20,
		Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{legacyRule}},
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_plans SET enabled=TRUE WHERE id=$1`, legacy.ID)
	require.NoError(t, err)
	legacyResult, err := repo.Create(ctx, &service.ScheduledTestResult{
		PlanID: legacy.ID, TestDefinitionID: &candyID, GroupID: &legacyGroupID,
		AccountID: &accountID, TargetMode: legacy.TargetMode, ModelID: legacy.ModelID,
		Status: "running", OutputKind: "number", StartedAt: round, FinishedAt: round,
	})
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtection(ctx, legacyResult, legacyRule))
	complete(legacyResult, "fail")
	require.Equal(t, []int64{10}, bindings(), "queued legacy group assignment cannot undo workflow ownership")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=$1`, accountID).Scan(&status))
	require.Equal(t, "quality_paused", status, "legacy scheduling holds still protect the account")
	require.NoError(t, repo.CompleteProtection(ctx, automatic, "pass", ""))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=$1`, accountID).Scan(&status))
	require.Equal(t, "quality_paused", status, "workflow group placement cannot release another rule's hold")
}
