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

// The actions fixture provides real group bindings, subscriptions, review state,
// ballots and scheduler events so visibility is tested with routing effects.
func testScheduledTestPublicVotingSwitch(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, definitionID int64) {
	t.Helper()
	groupID, accountID := int64(8), int64(62)
	rule := service.ScheduledTestProtectionRule{
		TestDefinitionID: definitionID,
		Vote:             &service.ScheduledTestVoteConfig{Enabled: true, RejectAbove: 1, PassAtLeast: 2},
	}
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Private until explicitly opened", GroupID: &groupID, GroupIDs: []int64{groupID},
		TargetMode: "all_accounts", TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{definitionID},
		ModelID: "model", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20,
		Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{rule}},
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE scheduled_test_plans SET latest_run_id='test-round' WHERE id=$1`, plan.ID)
	require.NoError(t, err)
	started := time.Now().UTC().Truncate(time.Microsecond)
	result, err := repo.Create(ctx, &service.ScheduledTestResult{
		RunID: "test-round", PlanID: plan.ID, TestDefinitionID: &definitionID, GroupID: &groupID, AccountID: &accountID,
		TargetMode: plan.TargetMode, ModelID: plan.ModelID, Status: "running", OutputKind: "html",
		StartedAt: started, FinishedAt: started,
	})
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtection(ctx, result, rule))
	result.Status, result.ResponseText, result.OutputHTML = "success", "safe output", "<svg>animation</svg>"
	require.NoError(t, repo.Update(ctx, result))
	require.NoError(t, repo.CompleteProtection(ctx, result, "pass", ""))

	public, err := repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, public, "legacy enabled voting remains administrator-only by default")
	_, err = repo.CastTestVote(ctx, 1, result.ID, "fail")
	require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
	reviews, err := repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	generation := reviews[0].Generation
	require.NoError(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "pass"))

	setPublic := func(enabled bool) {
		t.Helper()
		plan.Protection.Rules[0].Vote.PublicEnabled = enabled
		plan, err = plans.Update(ctx, plan)
		require.NoError(t, err)
		var currentGeneration, currentResultID int64
		var completed bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT generation,result_id,completed FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=$2`, plan.ID, accountID).Scan(&currentGeneration, &currentResultID, &completed))
		require.Equal(t, generation, currentGeneration, "opening or closing does not start a new round")
		require.Equal(t, result.ID, currentResultID)
		require.True(t, completed)
	}
	setPublic(true)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Len(t, public, 1, "opening exposes the already completed result immediately")
	require.Equal(t, result.ID, public[0].Result.ID)
	ballot, err := repo.CastTestVote(ctx, 1, result.ID, "fail")
	require.NoError(t, err)
	require.Equal(t, 1, ballot.Voting.FailCount)
	setPublic(false)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, public, "closing hides the active result immediately")
	_, err = repo.CastTestVote(ctx, 2, result.ID, "fail")
	require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable, "a remembered result URL cannot bypass the switch")
	reviews, err = repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, generation, reviews[0].Generation)
	require.Equal(t, "pass", reviews[0].AdminVerdict, "closing does not erase the administrator's decision")
	require.NoError(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "fail"), "administrator can still review while public voting is closed")
	setPublic(true)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Len(t, public, 1)
	require.Equal(t, 1, public[0].Voting.FailCount, "closing and reopening preserves this round's existing ballots")
	require.Equal(t, "fail", public[0].Voting.MyVote)
	reviews, err = repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Equal(t, "fail", reviews[0].AdminVerdict)
	queuedSnapshot, err := plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	setPublic(false)
	require.NoError(t, repo.BeginProtectionRun(ctx, queuedSnapshot, started.Add(time.Hour)), "a visibility-only edit must not invalidate a queued runner snapshot")
}

func testScheduledTestGenericPublicVotes(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	accountID, upperGroupID := int64(62), int64(10)
	rules := []service.ScheduledTestProtectionRule{
		{TestDefinitionID: candyID, ExpectedAnswer: "21", AnswerMatch: "numeric", OnPass: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{10}}, OnFail: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{8}}},
		{TestDefinitionID: pelicanID, Priority: 100, Vote: &service.ScheduledTestVoteConfig{Enabled: true, PublicEnabled: true, RejectAbove: 1, PassAtLeast: 2}, OnPass: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{10}}, OnFail: &service.ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{8}}},
	}
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Public review policy", SortOrder: 1, GroupIDs: []int64{10, 8}, GroupID: &upperGroupID, TargetMode: "all_accounts",
		TestDefinitionID: &candyID, TestDefinitionIDs: []int64{candyID, pelicanID},
		ModelID: "model", CronExpression: "0 * * * *", Enabled: true, MaxResults: 20,
		Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: rules},
	})
	require.NoError(t, err)
	_, err = db.ExecContext(ctx, `UPDATE groups SET is_exclusive=TRUE WHERE id IN (8,10);
INSERT INTO user_allowed_groups(user_id,group_id) SELECT user_id,group_id FROM generate_series(1,5) user_id CROSS JOIN (VALUES(8),(10)) groups(group_id);
INSERT INTO user_allowed_groups(user_id,group_id) VALUES(6,8),(7,10);
INSERT INTO user_subscriptions(user_id,group_id,status,starts_at,expires_at) VALUES(8,8,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '1 day'),(9,10,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '1 day')`)
	require.NoError(t, err)
	bindings := func() []int64 {
		t.Helper()
		var ids pq.Int64Array
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(array_agg(group_id ORDER BY group_id),'{}'::bigint[]) FROM account_groups WHERE account_id=$1`, accountID).Scan(&ids))
		return []int64(ids)
	}
	round := time.Now().UTC().Truncate(time.Microsecond)
	var roundResults []*service.ScheduledTestResult
	startRound := func() {
		t.Helper()
		round = round.Add(time.Hour)
		inputs := make([]*service.ScheduledTestResult, 0, len(plan.Protection.Rules))
		for index, rule := range plan.Protection.Rules {
			kind := "number"
			if index == 1 {
				kind = "html"
			}
			id := rule.TestDefinitionID
			inputs = append(inputs, &service.ScheduledTestResult{PlanID: plan.ID, TestDefinitionID: &id, GroupID: plan.GroupID, AccountID: &accountID, TargetMode: plan.TargetMode, ModelID: plan.ModelID, Status: "pending", OutputKind: kind, StartedAt: round, FinishedAt: round})
		}
		roundResults, err = repo.BeginRun(ctx, plan, round.Format(time.RFC3339Nano), inputs)
		require.NoError(t, err)
		require.NoError(t, repo.BeginProtectionRun(ctx, plan, round))
	}
	complete := func(index int, verdict string) *service.ScheduledTestResult {
		t.Helper()
		result := roundResults[index]
		require.NoError(t, repo.BeginProtection(ctx, result, plan.Protection.Rules[index]))
		result.Status, result.ResponseText = "success", "21"
		if index == 1 {
			result.OutputHTML = "<svg>animation</svg>"
		}
		require.NoError(t, repo.Update(ctx, result))
		require.NoError(t, repo.CompleteProtection(ctx, result, verdict, "automatic check result"))
		return result
	}
	cast := func(userID, resultID int64, vote string) *service.ScheduledTestVoteResult {
		t.Helper()
		row, err := repo.CastTestVote(ctx, userID, resultID, vote)
		require.NoError(t, err)
		return row
	}

	startRound()
	automatic := complete(0, "pass")
	require.Equal(t, []int64{10}, bindings(), "automatic placement still removes every unrelated membership")
	manual := complete(1, "pass")
	assertCurrentGroupViews := func(groupID int64) {
		t.Helper()
		groupName := "Source and lower tier"
		groupOrder := 1
		allowedUser, previousGroupUser := int64(6), int64(7)
		if groupID == 10 {
			groupName = "Upper tier one"
			groupOrder = 0
			allowedUser, previousGroupUser = 7, 6
		}
		assertOrder := func(row *service.ScheduledTestResult) {
			t.Helper()
			require.NotNil(t, row.GroupOrder)
			require.Equal(t, groupOrder, *row.GroupOrder, "display order follows the selected group order")
			require.Equal(t, 1, row.PlanOrder, "the result retains its producing plan's order")
		}
		for _, userID := range []int64{1, allowedUser, allowedUser + 2} {
			visible, err := repo.ListVisible(ctx, userID, 3)
			require.NoError(t, err)
			require.NotEmpty(t, visible)
			for _, row := range visible {
				require.Equal(t, groupID, *row.GroupID, "existing result cards follow the current single group without another detection")
				require.Equal(t, groupName, row.GroupName)
				assertOrder(row)
			}
			history, err := repo.ListVisibleHistory(ctx, userID, manual.ID, 0, 20)
			require.NoError(t, err)
			require.NotEmpty(t, history)
			for _, row := range history {
				require.Equal(t, groupID, *row.GroupID, "history follows the same live group projection")
				require.Equal(t, groupName, row.GroupName)
				assertOrder(row)
			}
			votes, err := repo.ListVotingResults(ctx, userID)
			require.NoError(t, err)
			require.Len(t, votes, 1)
			require.Equal(t, groupID, *votes[0].Result.GroupID)
			require.Equal(t, groupName, votes[0].Result.GroupName)
			assertOrder(votes[0].Result)
		}
		reviews, err := repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Len(t, reviews, 1)
		require.Equal(t, groupID, *reviews[0].Result.GroupID)
		require.Equal(t, groupName, reviews[0].Result.GroupName)
		assertOrder(reviews[0].Result)
		for _, userID := range []int64{previousGroupUser, previousGroupUser + 2} {
			previousVisible, err := repo.ListVisible(ctx, userID, 3)
			require.NoError(t, err)
			require.Empty(t, previousVisible, "access or subscription to the old group does not grant access after movement")
			_, err = repo.ListVisibleHistory(ctx, userID, manual.ID, 0, 20)
			require.ErrorIs(t, err, sql.ErrNoRows)
			previousVotes, err := repo.ListVotingResults(ctx, userID)
			require.NoError(t, err)
			require.Empty(t, previousVotes)
			_, err = repo.CastTestVote(ctx, userID, manual.ID, "fail")
			require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		}
		stored, err := repo.GetByID(ctx, manual.ID)
		require.NoError(t, err)
		require.Equal(t, upperGroupID, *stored.GroupID, "live projection leaves the original execution record intact")
	}
	assertCurrentGroupViews(10)
	public, err := repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Len(t, public, 1)
	require.Equal(t, manual.ID, public[0].Result.ID)
	require.Equal(t, int64(10), *public[0].Result.GroupID, "voting follows current placement")
	cast(1, manual.ID, "fail")
	require.Equal(t, []int64{10}, bindings(), "one failure does not exceed reject_above=1")
	cast(2, manual.ID, "fail")
	require.Equal(t, []int64{8}, bindings(), "two failures replace all memberships with the lower tier")
	assertCurrentGroupViews(8)
	cast(1, manual.ID, "pass")
	require.Equal(t, []int64{10}, bindings(), "pending review permits the automatic tier to apply")
	cast(2, manual.ID, "pass")
	require.Equal(t, []int64{10}, bindings(), "two passes reach the promotion threshold")
	assertCurrentGroupViews(10)
	require.NoError(t, repo.CompleteProtection(ctx, automatic, "fail", "late automatic completion"))
	require.Equal(t, []int64{10}, bindings(), "same-round conclusive public review outranks late automatic completion")
	reviews, err := repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	generation := reviews[0].Generation
	require.NoError(t, repo.DecideTestResult(ctx, 1, manual.ID, generation, "fail"))
	require.Equal(t, []int64{8}, bindings(), "administrator failure overrides passing public votes")
	assertCurrentGroupViews(8)
	cast(3, manual.ID, "pass")
	require.Equal(t, []int64{8}, bindings(), "more public votes do not erase the administrator failure")
	require.NoError(t, repo.CompleteProtection(ctx, automatic, "pass", "late automatic completion"))
	require.Equal(t, []int64{8}, bindings())
	require.NoError(t, repo.DecideTestResult(ctx, 1, manual.ID, generation, "pass"))
	cast(3, manual.ID, "fail")
	cast(4, manual.ID, "fail")
	require.Equal(t, []int64{10}, bindings(), "administrator pass overrides a rejecting public majority")
	assertCurrentGroupViews(10)

	plan.Protection.Rules[1].Vote.PublicEnabled = false
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, public)
	_, err = repo.CastTestVote(ctx, 5, manual.ID, "fail")
	require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
	reviews, err = repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 1)
	require.Equal(t, generation, reviews[0].Generation)
	require.Equal(t, "pass", reviews[0].AdminVerdict)
	require.Equal(t, []int64{10}, bindings(), "closing strategy voting preserves the current group and administrator decision")
	plan.Protection.Rules[1].Vote.PublicEnabled = true
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Len(t, public, 1)
	require.Equal(t, 2, public[0].Voting.FailCount)
	require.Equal(t, 2, public[0].Voting.PassCount)

	startRound()
	_, err = repo.CastTestVote(ctx, 5, manual.ID, "pass")
	require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
	require.ErrorIs(t, repo.DecideTestResult(ctx, 1, manual.ID, generation, "pass"), service.ErrScheduledTestVoteUnavailable)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Empty(t, public, "the old hourly review disappears as soon as the next round starts")
	automatic = complete(0, "fail")
	require.Equal(t, []int64{8}, bindings(), "fresh automatic failure replaces previous administrator pass")
	manual = complete(1, "pass")
	assertCurrentGroupViews(8)
	public, err = repo.ListVotingResults(ctx, 1)
	require.NoError(t, err)
	require.Len(t, public, 1)
	require.Zero(t, public[0].Voting.PassCount)
	require.Zero(t, public[0].Voting.FailCount)
	require.Empty(t, public[0].Voting.MyVote)
	cast(1, manual.ID, "pass")
	require.Equal(t, []int64{8}, bindings(), "previous-round ballots do not contribute to this round's threshold")
	cast(2, manual.ID, "pass")
	require.Equal(t, []int64{10}, bindings(), "fresh public passes may override this round's automatic failure")
	assertCurrentGroupViews(10)
	require.NoError(t, repo.CompleteProtection(ctx, automatic, "fail", "late automatic completion"))
	require.Equal(t, []int64{10}, bindings())
}
