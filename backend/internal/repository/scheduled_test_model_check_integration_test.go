//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestModelCheckIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("quality_model_check_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	}()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	params := parsed.Query()
	params.Set("search_path", schema)
	parsed.RawQuery = params.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	exec := func(t *testing.T, statement string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, statement, args...)
		require.NoError(t, err)
	}
	exec(t, `CREATE TABLE users(id BIGINT PRIMARY KEY);
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,extra JSONB NOT NULL DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ,is_exclusive BOOLEAN NOT NULL DEFAULT FALSE,subscription_type TEXT NOT NULL DEFAULT 'standard');
CREATE TABLE account_groups(account_id BIGINT NOT NULL REFERENCES accounts(id),group_id BIGINT NOT NULL REFERENCES groups(id),priority INTEGER NOT NULL DEFAULT 50,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),PRIMARY KEY(account_id,group_id));
CREATE TABLE user_allowed_groups(user_id BIGINT,group_id BIGINT);
CREATE TABLE user_subscriptions(user_id BIGINT,group_id BIGINT,deleted_at TIMESTAMPTZ,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ);
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_model_check_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO users(id) VALUES(1),(2);
INSERT INTO accounts(id,name) VALUES(62,'Private upstream account name');
INSERT INTO groups(id,name) VALUES(8,'Source tier'),(10,'Higher tier'),(11,'Unrelated membership');`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql", "249_scheduled_test_result_reasoning_effort.sql",
		"250_allow_group_account_scheduled_test_targets.sql", "251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql", "256_scheduled_test_multiple_definitions.sql", "257_scheduled_test_hourly_statistics.sql",
		"258_scheduled_test_protection.sql", "259_scheduled_test_outcome_actions.sql",
		"260_scheduled_test_model_check.sql", "260_scheduled_test_model_check.sql",
		"261_scheduled_test_admin_review.sql",
		"262_scheduled_test_execution_snapshot.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		exec(t, string(raw))
	}
	var definitionID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='model_check'`).Scan(&definitionID))
	plans := &scheduledTestPlanRepository{db: db}
	results := &scheduledTestResultRepository{db: db}
	svc := service.NewScheduledTestService(plans, results)
	accountID, groupID := int64(62), int64(8)
	reset := func(t *testing.T) {
		exec(t, `TRUNCATE scheduled_test_plans,scheduled_test_results,scheduled_test_plan_definitions,scheduled_test_protection_states,scheduled_test_managed_accounts,scheduled_test_votes,scheduler_outbox RESTART IDENTITY CASCADE;
UPDATE accounts SET status='active',schedulable=TRUE,deleted_at=NULL,extra='{}';
UPDATE groups SET status='active',is_exclusive=FALSE,deleted_at=NULL;
TRUNCATE user_allowed_groups,user_subscriptions,account_groups;
INSERT INTO account_groups(account_id,group_id) VALUES(62,8),(62,11);`)
	}
	newPlan := func(t *testing.T, rule *service.ScheduledTestProtectionRule) *service.ScheduledTestPlan {
		t.Helper()
		input := &service.ScheduledTestPlan{Name: "Model evidence", AccountID: &accountID, GroupID: &groupID, TargetMode: "account", TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{definitionID}, ModelID: "public-model-alias", CronExpression: "* * * * *", Enabled: true, MaxResults: 10}
		if rule != nil {
			input.Protection = service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{*rule}}
		}
		plan, err := plans.Create(ctx, input)
		require.NoError(t, err)
		return plan
	}
	sequence := int64(0)
	persist := func(t *testing.T, plan *service.ScheduledTestPlan, status string, snapshot *service.ScheduledTestModelCheck) *service.ScheduledTestResult {
		t.Helper()
		sequence++
		started := time.Now().UTC().Add(time.Duration(sequence) * time.Second).Truncate(time.Microsecond)
		raw, err := json.Marshal(snapshot)
		require.NoError(t, err)
		row, err := results.Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, TestDefinitionID: &definitionID, GroupID: plan.GroupID, AccountID: plan.AccountID, TargetMode: plan.TargetMode, ModelID: plan.ModelID, Status: status, OutputKind: "model_check", ResponseText: string(raw), StartedAt: started, FinishedAt: started})
		require.NoError(t, err)
		return row
	}
	assertSnapshot := func(t *testing.T, result *service.ScheduledTestResult, expected service.ScheduledTestModelCheck, public bool) {
		t.Helper()
		require.Equal(t, "model_check", result.OutputKind)
		require.Empty(t, result.ResponseText)
		require.Empty(t, result.OutputHTML)
		require.Nil(t, result.OutputNumeric)
		require.Equal(t, &expected, result.OutputModelCheck)
		encoded, err := json.Marshal(result)
		require.NoError(t, err)
		require.Contains(t, string(encoded), `"output_model_check":`)
		if public {
			require.Empty(t, result.AccountName)
			require.NotContains(t, string(encoded), "Private upstream account name")
			require.NotContains(t, string(encoded), `"account_name"`)
			require.Equal(t, &accountID, result.AccountID)
		}
	}

	t.Run("model check definition migration is idempotent", func(t *testing.T) {
		var count int
		var kind, prompt string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_definitions WHERE key='model_check'`).Scan(&count))
		require.Equal(t, 1, count)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT output_kind,prompt FROM scheduled_test_definitions WHERE id=$1`, definitionID).Scan(&kind, &prompt))
		require.Equal(t, "model_check", kind)
		require.Equal(t, "Please reply with OK.", prompt)
	})

	t.Run("admin public and paginated history expose normalized model evidence including mismatches", func(t *testing.T) {
		reset(t)
		plan := newPlan(t, nil)
		checks := []service.ScheduledTestModelCheck{
			{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-sol"}, MatchMode: "exact", Verdict: "pass", Reason: "match"},
			{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-sol-2026-09-21"}, MatchMode: "snapshot", Verdict: "pass", Reason: "match"},
			{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{}, MatchMode: "exact", Verdict: "unknown", Reason: "missing_model"},
			{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-terra"}, MatchMode: "exact", Verdict: "fail", Reason: "mismatch"},
		}
		stored := make([]*service.ScheduledTestResult, 0, len(checks))
		for i := range checks {
			stored = append(stored, persist(t, plan, "success", &checks[i]))
		}
		persist(t, plan, "failed", &service.ScheduledTestModelCheck{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{}, MatchMode: "exact", Verdict: "unknown", Reason: "upstream_error"})
		adminRows, err := svc.ListResults(ctx, plan.ID, 10)
		require.NoError(t, err)
		require.Len(t, adminRows, 5)
		require.Equal(t, "failed", adminRows[0].Status)
		require.Equal(t, "upstream_error", adminRows[0].OutputModelCheck.Reason)
		for i := range checks {
			row := adminRows[len(checks)-i]
			assertSnapshot(t, row, checks[i], false)
			require.Equal(t, "Private upstream account name", row.AccountName)
		}
		visible, err := svc.ListVisibleResults(ctx, 1, 3)
		require.NoError(t, err)
		require.Len(t, visible, 3)
		require.Equal(t, stored[3].ID, visible[0].ID)
		require.Equal(t, "success", visible[0].Status, "a model mismatch is a completed request, so it must stay visible")
		for i, row := range visible {
			assertSnapshot(t, row, checks[len(checks)-1-i], true)
		}
		firstPage, err := svc.ListVisibleResultHistory(ctx, 1, stored[3].ID, 0, 2)
		require.NoError(t, err)
		require.Len(t, firstPage.Items, 2)
		require.NotNil(t, firstPage.NextBeforeID)
		assertSnapshot(t, firstPage.Items[0], checks[3], true)
		assertSnapshot(t, firstPage.Items[1], checks[2], true)
		secondPage, err := svc.ListVisibleResultHistory(ctx, 1, stored[3].ID, *firstPage.NextBeforeID, 2)
		require.NoError(t, err)
		require.Len(t, secondPage.Items, 2)
		require.Nil(t, secondPage.NextBeforeID)
		assertSnapshot(t, secondPage.Items[0], checks[1], true)
		assertSnapshot(t, secondPage.Items[1], checks[0], true)
		var raw string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT response_text FROM scheduled_test_results WHERE id=$1`, stored[3].ID).Scan(&raw))
		require.NotEmpty(t, raw, "normalizing public output must preserve the persisted evidence")
	})

	t.Run("stored verdict is rechecked and malformed evidence remains unknown", func(t *testing.T) {
		reset(t)
		plan := newPlan(t, nil)
		row := persist(t, plan, "success", &service.ScheduledTestModelCheck{RequestedModel: "wrong-stored-request", UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-terra"}, MatchMode: "exact", Verdict: "pass", Reason: "match"})
		visible, err := svc.ListVisibleResults(ctx, 1, 3)
		require.NoError(t, err)
		require.Len(t, visible, 1)
		assertSnapshot(t, visible[0], service.ScheduledTestModelCheck{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-terra"}, MatchMode: "exact", Verdict: "fail", Reason: "mismatch"}, true)
		exec(t, `UPDATE scheduled_test_results SET response_text='not JSON',output_html='<p>old renderer</p>',output_numeric=9 WHERE id=$1`, row.ID)
		visible, err = svc.ListVisibleResults(ctx, 1, 3)
		require.NoError(t, err)
		require.Len(t, visible, 1)
		assertSnapshot(t, visible[0], service.ScheduledTestModelCheck{RequestedModel: plan.ModelID, ReturnedModels: []string{}, MatchMode: "exact", Verdict: "unknown", Reason: "invalid_evidence"}, true)
	})

	t.Run("model verdicts change configured tiers and missing evidence preserves the last tier", func(t *testing.T) {
		reset(t)
		rule := service.ScheduledTestProtectionRule{
			TestDefinitionID: definitionID, ModelMatch: "exact", PauseOnFailure: true,
			OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{10}},
			OnFail: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{8}},
		}
		plan := newPlan(t, &rule)
		apply := func(snapshot service.ScheduledTestModelCheck, verdict string) {
			t.Helper()
			result := persist(t, plan, "success", &snapshot)
			require.NoError(t, results.BeginProtection(ctx, result, rule))
			result.OutputModelCheck = &snapshot
			require.NoError(t, results.CompleteProtection(ctx, result, verdict, snapshot.Reason))
		}
		assertGroups := func(ids []int64) {
			t.Helper()
			var got pq.Int64Array
			require.NoError(t, db.QueryRowContext(ctx, `SELECT array_agg(group_id ORDER BY group_id) FROM account_groups WHERE account_id=62`).Scan(&got))
			require.Equal(t, ids, []int64(got))
		}
		matching := service.ScheduledTestModelCheck{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-sol"}, MatchMode: "exact", Verdict: "pass", Reason: "match"}
		missing := service.ScheduledTestModelCheck{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{}, MatchMode: "exact", Verdict: "unknown", Reason: "missing_model"}
		mismatch := service.ScheduledTestModelCheck{RequestedModel: plan.ModelID, UpstreamModel: "gpt-5.6-sol", ReturnedModels: []string{"gpt-5.6-terra"}, MatchMode: "exact", Verdict: "fail", Reason: "mismatch"}
		apply(matching, "pass")
		assertGroups([]int64{10, 11})
		apply(missing, "pending")
		assertGroups([]int64{10, 11})
		apply(mismatch, "fail")
		assertGroups([]int64{8, 11})
		visible, err := svc.ListVisibleResults(ctx, 1, 3)
		require.NoError(t, err)
		require.Len(t, visible, 3)
		assertSnapshot(t, visible[0], mismatch, true)
		require.Equal(t, "success", visible[0].Status)
		apply(missing, "pending")
		assertGroups([]int64{8, 11})
		apply(matching, "pass")
		assertGroups([]int64{10, 11})
		var status string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
		require.Equal(t, "active", status)
	})
}
