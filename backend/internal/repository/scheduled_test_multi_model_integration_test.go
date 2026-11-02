//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func multiModelProtectionRound(t *testing.T, ctx context.Context, repo *scheduledTestResultRepository, plan *service.ScheduledTestPlan, run string, at time.Time) map[string][]*service.ScheduledTestResult {
	t.Helper()
	var inputs []*service.ScheduledTestResult
	for _, model := range plan.SelectedModelIDs() {
		for _, definitionID := range plan.TestDefinitionIDs {
			accountID, definitionID := int64(62), definitionID
			inputs = append(inputs, &service.ScheduledTestResult{PlanID: plan.ID, GroupID: plan.GroupID, AccountID: &accountID, TestDefinitionID: &definitionID, TargetMode: plan.TargetMode, ModelID: model, ReasoningEffort: plan.ReasoningEffort, Status: "pending", OutputKind: "number", StartedAt: at, FinishedAt: at})
		}
	}
	rows, err := repo.BeginRun(ctx, plan, run, inputs)
	require.NoError(t, err)
	require.NoError(t, repo.BeginProtectionRun(ctx, plan, at))
	byModel := map[string][]*service.ScheduledTestResult{}
	for _, row := range rows {
		require.NoError(t, repo.BeginProtection(ctx, row, *plan.ProtectionRule(row.TestDefinitionID)))
		byModel[row.ModelID] = append(byModel[row.ModelID], row)
	}
	return byModel
}

func completeMultiModelProtection(t *testing.T, ctx context.Context, repo *scheduledTestResultRepository, row *service.ScheduledTestResult, verdict string) {
	t.Helper()
	row.Status, row.ResponseText = "success", "21"
	if verdict == "fail" {
		row.ResponseText = "20"
	}
	require.NoError(t, repo.Update(ctx, row))
	require.NoError(t, repo.CompleteProtection(ctx, row, verdict, "fixture "+verdict))
}

func checkMultiModelProtectionStates(t *testing.T, ctx context.Context, db *sql.DB, expected map[string]string) {
	t.Helper()
	var raw []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE(extra->'active_test_model_states','{}'::jsonb) FROM accounts WHERE id=62`).Scan(&raw))
	var states map[string]struct {
		Status string `json:"status"`
		Reason string `json:"reason"`
	}
	require.NoError(t, json.Unmarshal(raw, &states))
	require.Len(t, states, len(expected))
	for model, want := range expected {
		require.Equal(t, want, states[model].Status, "model %s status", model)
		if want == "blocked" {
			require.NotEmpty(t, states[model].Reason)
		}
	}
}

func testScheduledTestModelProjectionLifecycle(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, definitionID int64) {
	t.Helper()
	create := func(model string) *service.ScheduledTestPlan {
		plan := genericRoutingPlan(definitionID, 8)
		plan.ModelID, plan.ModelIDs = model, []string{model}
		plan.Protection.Rules[0].OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
		plan.Protection.Rules[0].OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
		created, err := plans.Create(ctx, plan)
		require.NoError(t, err)
		return created
	}
	failed, healthy := create("model-a"), create("model-b")
	checkStatus := func(want string) {
		t.Helper()
		var got string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&got))
		require.Equal(t, want, got)
	}
	rows := multiModelProtectionRound(t, ctx, repo, failed, "lifecycle-failure", time.Now().UTC().Truncate(time.Microsecond))
	completeMultiModelProtection(t, ctx, repo, rows["model-a"][0], "fail")
	checkStatus("active")
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "pending"})
	// Upgrade a populated pre-projection database, then repeat the additive
	// migration to verify stable data and preserved manual controls.
	_, err := db.ExecContext(ctx, `UPDATE accounts SET extra=extra-'active_test_model_states'; UPDATE accounts SET status='inactive',schedulable=FALSE WHERE id=63`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("333_active_test_model_states.sql")
	require.NoError(t, err)
	for attempt := 0; attempt < 2; attempt++ {
		_, err = db.ExecContext(ctx, string(migration))
		require.NoError(t, err)
		checkStatus("active")
		checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "pending"})
		var status string
		var schedulable bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status,schedulable FROM accounts WHERE id=63`).Scan(&status, &schedulable))
		require.Equal(t, "inactive", status)
		require.False(t, schedulable)
	}
	// The healthy policy has never produced state rows. Its configuration still
	// belongs to the monitored model set, including during enable/disable edits.
	healthy.Enabled = false
	healthy, err = plans.Update(ctx, healthy)
	require.NoError(t, err)
	checkStatus("quality_paused")
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked"})
	healthy.Enabled = true
	healthy, err = plans.Update(ctx, healthy)
	require.NoError(t, err)
	checkStatus("active")
	require.NoError(t, plans.Delete(ctx, healthy.ID))
	checkStatus("quality_paused")
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked"})
	// A raw cascading deletion must project the same result as the Go writer.
	_, err = db.ExecContext(ctx, `DELETE FROM scheduled_test_plans WHERE id=$1`, failed.ID)
	require.NoError(t, err)
	checkStatus("active")
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{})
}

func testScheduledTestAllPassModelProjectionManualVeto(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	plan := genericRoutingPlan(candyID, 8)
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a"}
	plan.TestDefinitionIDs = []int64{candyID, pelicanID}
	plan.Protection.Mode = "all_pass"
	plan.Protection.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
	plan.Protection.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
	plan.Protection.Rules = append(plan.Protection.Rules, service.ScheduledTestProtectionRule{TestDefinitionID: pelicanID, Vote: &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}})
	plan, err := plans.Create(ctx, plan)
	require.NoError(t, err)
	rows := multiModelProtectionRound(t, ctx, repo, plan, "manual-display-status", time.Now().UTC().Truncate(time.Microsecond))
	completeMultiModelProtection(t, ctx, repo, rows["model-a"][0], "pass")
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "normal"})
	// The manual item has no current pass, but only an explicit rejection is a
	// veto in all-pass mode. The SQL cascade projection must use that same rule.
	_, err = db.ExecContext(ctx, `SELECT reconcile_active_test_model_protection(62)`)
	require.NoError(t, err)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "normal"})
}

func testScheduledTestAllPassMultiModelConsecutive(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID int64) {
	plan := genericRoutingPlan(candyID, 8, 10)
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a", "model-b"}
	plan.Protection.Mode = "all_pass"
	plan.Protection.RequiredConsecutivePasses = 2
	plan.Protection.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{10}}
	var err error
	plan, err = plans.Create(ctx, plan)
	require.NoError(t, err)
	at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	first := multiModelProtectionRound(t, ctx, repo, plan, "streak-model-first", at)
	completeMultiModelProtection(t, ctx, repo, first["model-a"][0], "pass")
	completeMultiModelProtection(t, ctx, repo, first["model-b"][0], "fail")
	second := multiModelProtectionRound(t, ctx, repo, plan, "streak-model-second", at.Add(time.Minute))
	completeMultiModelProtection(t, ctx, repo, second["model-a"][0], "pass")
	completeMultiModelProtection(t, ctx, repo, second["model-b"][0], "pass")
	var groups pq.Int64Array
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&groups))
	require.Equal(t, []int64{8, 9}, []int64(groups), "model-b's one pass cannot borrow model-a's two passes")
	third := multiModelProtectionRound(t, ctx, repo, plan, "streak-model-third", at.Add(2*time.Minute))
	completeMultiModelProtection(t, ctx, repo, third["model-b"][0], "pass")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&groups))
	require.Equal(t, []int64{8, 9}, []int64(groups), "the current model-a round must also complete")
	completeMultiModelProtection(t, ctx, repo, third["model-a"][0], "pass")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&groups))
	require.Equal(t, []int64{10}, []int64(groups))
}

func testScheduledTestMultiModelProtection(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, _ int64, mode string) {
	t.Helper()
	plan := genericRoutingPlan(candyID, 8, 10)
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a", "model-b"}
	plan.Protection.Mode = mode
	pass := &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{10}}
	fail := &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{8}}
	plan.Protection.Rules[0].OnPass, plan.Protection.Rules[0].OnFail = pass, fail
	if mode == "all_pass" {
		plan.Protection.OnPass, plan.Protection.OnFail = pass, fail
	}
	if mode == "combined" {
		plan.Protection.Combinations = []service.ScheduledTestCombinationRule{
			{ID: "pass", Name: "Pass", Priority: 1, Condition: combinedTestLeaf(candyID, "pass"), Action: *pass},
			{ID: "fail", Name: "Fail", Priority: 1, Condition: combinedTestLeaf(candyID, "fail"), Action: *fail},
		}
	}
	var err error
	plan, err = plans.Create(ctx, plan)
	require.NoError(t, err)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "pending", "model-b": "pending"})
	started := time.Now().UTC().Truncate(time.Microsecond)
	first := multiModelProtectionRound(t, ctx, repo, plan, "models-first", started)
	check := func(status string, groups ...int64) {
		t.Helper()
		var got string
		var bindings pq.Int64Array
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&got))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&bindings))
		require.Equal(t, status, got)
		require.Equal(t, groups, []int64(bindings))
		var allModelsPaused bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COALESCE((extra->>'active_test_all_models_paused')::boolean,FALSE) FROM accounts WHERE id=62`).Scan(&allModelsPaused))
		require.Equal(t, status == "quality_paused", allModelsPaused)
	}
	completeMultiModelProtection(t, ctx, repo, first["model-a"][0], "fail")
	check("active", 8, 9)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "pending"})
	completeMultiModelProtection(t, ctx, repo, first["model-b"][0], "pass")
	check("active", 8, 9)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "normal"})
	var modelHolds map[string]string
	var rawHolds []byte
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extra->'active_test_model_holds' FROM accounts WHERE id=62`).Scan(&rawHolds))
	require.NoError(t, json.Unmarshal(rawHolds, &modelHolds))
	require.Contains(t, modelHolds, "model-a")
	require.NotContains(t, modelHolds, "model-b", "a healthy model remains schedulable on the active account")
	var stateCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=62 AND completed`, plan.ID).Scan(&stateCount))
	require.Equal(t, 2, stateCount, "each model keeps its own current result")
	for model, verdict := range map[string]string{"model-a": "fail", "model-b": "pass"} {
		var got string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT verdict FROM scheduled_test_protection_states WHERE plan_id=$1 AND account_id=62 AND test_definition_id=$2 AND model_key=$3`, plan.ID, candyID, model).Scan(&got))
		require.Equal(t, verdict, got)
	}
	latest, err := repo.ListByPlanID(ctx, plan.ID, 1)
	require.NoError(t, err)
	require.Len(t, latest, 2, "admin latest view retains each model")
	public, err := repo.ListVisible(ctx, 1, 1)
	require.NoError(t, err)
	require.Len(t, public, 2, "users can switch between both model results")
	for _, results := range [][]*service.ScheduledTestResult{latest, public} {
		for _, result := range results {
			require.NotNil(t, result.AccountSchedulable)
			require.True(t, *result.AccountSchedulable, "the account remains available for its healthy model")
			require.NotNil(t, result.ModelSchedulable)
			require.Equal(t, result.ModelID == "model-b", *result.ModelSchedulable, "the failed model must show as unavailable independently of the healthy model")
		}
	}
	for _, anchor := range public {
		history, err := repo.ListVisibleHistory(ctx, 1, anchor.ID, 0, 20)
		require.NoError(t, err)
		require.Len(t, history, 1)
		require.Equal(t, anchor.ModelID, history[0].ModelID)
	}
	next := multiModelProtectionRound(t, ctx, repo, plan, "models-next", started.Add(time.Minute))
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "pending"})
	// A healthy different model still cannot clear model-a's previous hold.
	completeMultiModelProtection(t, ctx, repo, next["model-b"][0], "pass")
	check("active", 8, 9)
	completeMultiModelProtection(t, ctx, repo, next["model-a"][0], "pass")
	check("active", 10)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "normal", "model-b": "normal"})
	third := multiModelProtectionRound(t, ctx, repo, plan, "models-third", started.Add(2*time.Minute))
	completeMultiModelProtection(t, ctx, repo, third["model-b"][0], "fail")
	check("active", 10)
	completeMultiModelProtection(t, ctx, repo, third["model-a"][0], "fail")
	check("quality_paused", 8)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "blocked", "model-b": "blocked"})
	fourth := multiModelProtectionRound(t, ctx, repo, plan, "models-fourth", started.Add(3*time.Minute))
	completeMultiModelProtection(t, ctx, repo, fourth["model-a"][0], "pass")
	check("active", 8)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": "normal", "model-b": "blocked"})
	// Removing leaf evidence makes that model pending, while a separately
	// persisted all-pass round still supplies genuine passing evidence.
	require.NoError(t, repo.Delete(ctx, fourth["model-a"][0].ID))
	recoveredStatus := "pending"
	if mode == "all_pass" {
		recoveredStatus = "normal"
	}
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": recoveredStatus, "model-b": "blocked"})
	require.NoError(t, repo.Delete(ctx, fourth["model-b"][0].ID))
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{"model-a": recoveredStatus, "model-b": "blocked"})
	require.NoError(t, db.QueryRowContext(ctx, `SELECT extra->'active_test_model_holds' FROM accounts WHERE id=62`).Scan(&rawHolds))
	require.NoError(t, json.Unmarshal(rawHolds, &modelHolds))
	require.Contains(t, modelHolds, "model-b")
	plan.Enabled = false
	_, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	check("active", 8)
	checkMultiModelProtectionStates(t, ctx, db, map[string]string{})
	for _, table := range []string{"scheduled_test_protection_states", "scheduled_test_combination_states"} {
		require.NoError(t, db.QueryRowContext(ctx, fmt.Sprintf("SELECT COUNT(*) FROM %s WHERE plan_id=$1", table), plan.ID).Scan(&stateCount))
		require.Zero(t, stateCount)
	}
}

func testScheduledTestMultiModelCombinationEvidence(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, candyID, pelicanID int64) {
	t.Helper()
	plan := combinedRoutingPlan(candyID, pelicanID)
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a", "model-b"}
	plan.Protection.Combinations = plan.Protection.Combinations[:1]
	var err error
	plan, err = plans.Create(ctx, plan)
	require.NoError(t, err)
	rows := multiModelProtectionRound(t, ctx, repo, plan, "models-no-mixed-expression", time.Now().UTC().Truncate(time.Microsecond))
	completeMultiModelProtection(t, ctx, repo, rows["model-a"][0], "pass")
	completeMultiModelProtection(t, ctx, repo, rows["model-b"][1], "pass")
	var bindings pq.Int64Array
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&bindings))
	require.Equal(t, []int64{8, 9}, []int64(bindings), "one pass from each model cannot satisfy a single model's ALL expression")
	completeMultiModelProtection(t, ctx, repo, rows["model-a"][1], "pass")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&bindings))
	require.Equal(t, []int64{8, 9}, []int64(bindings), "pending other model prevents global promotion")
	completeMultiModelProtection(t, ctx, repo, rows["model-b"][0], "pass")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&bindings))
	require.Equal(t, []int64{10}, []int64(bindings))
}

func testScheduledTestMultiModelAdministratorReview(t *testing.T, ctx context.Context, db *sql.DB, plans *scheduledTestPlanRepository, repo *scheduledTestResultRepository, pelicanID int64) {
	t.Helper()
	_, err := db.ExecContext(ctx, `UPDATE quality_operations_config SET config=jsonb_set(config,'{pelican_review}', '{"enabled":true,"judge_account_id":63,"judge_model":"vision","daily_requests":100}'::jsonb)`)
	require.NoError(t, err)
	plan := genericRoutingPlan(pelicanID, 8)
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a", "model-b"}
	rule := &plan.Protection.Rules[0]
	rule.ExpectedAnswer, rule.AnswerMatch = "", ""
	rule.AutoReview = &service.ScheduledTestAutoReviewConfig{Enabled: true}
	rule.Vote = &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}
	rule.OnPass = &service.ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
	rule.OnFail = &service.ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
	rule.PassRate = &service.ScheduledTestPassRateConfig{Enabled: true, WindowSize: 2, WindowSeconds: 604800, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 2}
	plan, err = plans.Create(ctx, plan)
	require.NoError(t, err)
	started := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
	var latest map[string][]*service.ScheduledTestResult
	for round := 0; round < 2; round++ {
		latest = multiModelProtectionRound(t, ctx, repo, plan, fmt.Sprintf("review-models-%d", round), started.Add(time.Duration(round)*time.Minute))
		for _, rows := range latest {
			row := rows[0]
			row.OutputKind, row.OutputHTML, row.Status = "html", "<svg/>", "success"
			row.ExecutionInfo = &service.ScheduledTestExecutionInfo{AutoReview: &service.ScheduledTestAutoReviewSummary{Verdict: "pass", JudgeAccountID: 63, JudgeModel: "vision", RubricVersion: service.ScheduledTestReviewRubricVersion}}
			require.NoError(t, repo.Update(ctx, row))
			require.NoError(t, repo.CompleteProtection(ctx, row, "pass", "automatic pass"))
		}
	}
	reviews, err := repo.ListAdminReviews(ctx)
	require.NoError(t, err)
	require.Len(t, reviews, 2, "both models must retain a usable administrator review")
	generations := map[string]int64{}
	for _, review := range reviews {
		generations[review.Result.ModelID] = review.Generation
		require.Equal(t, map[string]int{"model-a": 0, "model-b": 1}[review.Result.ModelID], review.Result.ModelOrder)
	}
	decide := func(model, verdict string) {
		t.Helper()
		require.NoError(t, repo.DecideTestResult(ctx, 1, latest[model][0].ID, generations[model], verdict))
	}
	summary := func(model string) service.ScheduledTestPassRateSummary {
		t.Helper()
		var raw []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT summary FROM scheduled_test_pass_rate_states WHERE plan_id=$1 AND account_id=62 AND test_definition_id=$2 AND model_key=$3`, plan.ID, pelicanID, model).Scan(&raw))
		var result service.ScheduledTestPassRateSummary
		require.NoError(t, json.Unmarshal(raw, &result))
		return result
	}
	decide("model-a", "fail")
	require.Equal(t, 1, summary("model-a").Passed)
	require.True(t, summary("model-a").Blocked)
	require.Equal(t, 2, summary("model-b").Passed)
	require.False(t, summary("model-b").Blocked)
	decide("model-b", "pass")
	var status string
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "active", status, "only the failed model is disabled")
	decide("model-b", "fail")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "quality_paused", status, "the account pauses only when both monitored models fail")
	decide("model-a", "pass")
	require.Equal(t, 2, summary("model-a").Passed)
	require.False(t, summary("model-a").Blocked)
	require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "active", status, "recovering one model makes the account usable for that model")
	require.True(t, summary("model-b").Blocked)
	decide("model-b", "pass")
	require.False(t, summary("model-b").Blocked)
	var evidence int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_pass_rate_evidence WHERE plan_id=$1`, plan.ID).Scan(&evidence))
	require.Equal(t, 4, evidence, "two models across two rounds produce four independent observations")
}
