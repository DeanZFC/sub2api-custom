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

func TestScheduledTestQualityIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer adminDB.Close()
	schema := fmt.Sprintf("quality_test_%d", time.Now().UnixNano())
	_, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := adminDB.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	}()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	execSQL := func(statement string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, statement, args...)
		require.NoError(t, err)
	}
	applyMigration := func(name string) {
		t.Helper()
		data, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		execSQL(string(data))
	}
	execSQL(`CREATE TABLE accounts (id BIGINT PRIMARY KEY, deleted_at TIMESTAMPTZ);
		CREATE TABLE groups (id BIGINT PRIMARY KEY, name TEXT, status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, is_exclusive BOOLEAN DEFAULT false, subscription_type TEXT DEFAULT 'standard');
		CREATE TABLE account_groups (account_id BIGINT, group_id BIGINT);
		CREATE TABLE user_allowed_groups (user_id BIGINT, group_id BIGINT);
		CREATE TABLE user_subscriptions (user_id BIGINT, group_id BIGINT, deleted_at TIMESTAMPTZ, status TEXT, starts_at TIMESTAMPTZ, expires_at TIMESTAMPTZ);
		INSERT INTO accounts (id) VALUES (62), (63);
		INSERT INTO groups (id, name, is_exclusive) VALUES (8, 'Public group', false), (9, 'Private group', true);
		INSERT INTO account_groups VALUES (62, 8), (63, 8), (62, 9);`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql",
		"249_scheduled_test_result_reasoning_effort.sql", "250_allow_group_account_scheduled_test_targets.sql",
		"251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql",
	} {
		applyMigration(name)
	}
	var candyID, htmlID int64
	require.NoError(t, db.QueryRowContext(ctx, "SELECT id FROM scheduled_test_definitions WHERE key='candy'").Scan(&candyID))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT id FROM scheduled_test_definitions WHERE key='pelican'").Scan(&htmlID))
	var legacyPlanID, legacyResultID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans (name, group_id, test_definition_id, target_mode) VALUES ('Legacy', 8, $1, 'group') RETURNING id`, candyID).Scan(&legacyPlanID))
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO scheduled_test_results (plan_id, group_id, account_id, status) VALUES ($1, 8, 62, 'success') RETURNING id`, legacyPlanID).Scan(&legacyResultID))
	applyMigration("256_scheduled_test_multiple_definitions.sql")
	plans := NewScheduledTestPlanRepository(db)
	results := NewScheduledTestResultRepository(db)
	definitions := NewScheduledTestDefinitionRepository(db)
	legacy, err := plans.GetByID(ctx, legacyPlanID)
	require.NoError(t, err)
	require.Equal(t, []int64{candyID}, legacy.TestDefinitionIDs)
	legacyResult, err := results.GetByID(ctx, legacyResultID)
	require.NoError(t, err)
	require.Equal(t, candyID, *legacyResult.TestDefinitionID)
	require.Equal(t, "group", legacyResult.TargetMode)

	groupID, privateGroupID, accountID := int64(8), int64(9), int64(62)
	plan, err := plans.Create(ctx, &service.ScheduledTestPlan{
		Name: "Multiple checks", GroupID: &groupID, TestDefinitionID: &candyID,
		TestDefinitionIDs: []int64{candyID, htmlID}, TestType: "quality", TargetMode: "all_accounts",
		ModelID: "model-a", ReasoningEffort: "high", CronExpression: "0 * * * *", Enabled: true, MaxResults: 4,
	})
	require.NoError(t, err)
	require.Equal(t, []int64{candyID, htmlID}, plan.TestDefinitionIDs)
	require.Error(t, definitions.Delete(ctx, htmlID), "a secondary selected definition must not be deleted")
	plan.TestDefinitionIDs = []int64{htmlID, candyID}
	plan.TestDefinitionID = &htmlID
	plan, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	applyMigration("256_scheduled_test_multiple_definitions.sql")
	plan, err = plans.GetByID(ctx, plan.ID)
	require.NoError(t, err)
	require.Equal(t, []int64{htmlID, candyID}, plan.TestDefinitionIDs)

	started := time.Now().UTC().Truncate(time.Second)
	createResult := func(definitionID int64, status, model, effort string, age int) *service.ScheduledTestResult {
		t.Helper()
		result, err := results.Create(ctx, &service.ScheduledTestResult{
			PlanID: plan.ID, TestDefinitionID: &definitionID, TargetMode: "all_accounts",
			GroupID: &groupID, AccountID: &accountID, Status: status, ModelID: model,
			ReasoningEffort: effort, OutputKind: "text", ResponseText: "Rendered output",
			StartedAt: started.Add(-time.Duration(age) * time.Minute), FinishedAt: started,
		})
		require.NoError(t, err)
		return result
	}
	for _, definitionID := range []int64{candyID, htmlID} {
		for i := 1; i <= 6; i++ {
			createResult(definitionID, "success", "model-a", "high", i)
		}
	}
	createResult(candyID, "success", "model-a", "medium", 1)
	createResult(candyID, "success", "model-b", "high", 1)
	failed := createResult(htmlID, "failed", "model-a", "high", 0)
	running := createResult(candyID, "running", "model-a", "high", 0)
	initialRunning := createResult(htmlID, "running", "model-c", "high", 0)
	privateResult := createResult(htmlID, "success", "private-model", "high", 1)
	privateResult.GroupID = &privateGroupID
	require.NoError(t, results.Update(ctx, privateResult))

	visible, err := results.ListVisible(ctx, 100, 4)
	require.NoError(t, err)
	seriesCounts := map[string]int{}
	seen := map[int64]bool{}
	for _, result := range visible {
		seen[result.ID] = true
		require.NotEqual(t, "failed", result.Status)
		if result.ID == legacyResultID {
			require.Nil(t, result.AccountID, "group results must not expose the executing account")
			continue
		}
		seriesCounts[fmt.Sprintf("%d/%s/%s", *result.TestDefinitionID, result.ModelID, result.ReasoningEffort)]++
	}
	require.Equal(t, 4, seriesCounts[fmt.Sprintf("%d/model-a/high", candyID)])
	require.Equal(t, 4, seriesCounts[fmt.Sprintf("%d/model-a/high", htmlID)])
	require.Equal(t, 1, seriesCounts[fmt.Sprintf("%d/model-a/medium", candyID)])
	require.Equal(t, 1, seriesCounts[fmt.Sprintf("%d/model-b/high", candyID)])
	require.True(t, seen[initialRunning.ID])
	require.False(t, seen[running.ID], "progress must not displace a previous success")
	require.False(t, seen[failed.ID])
	require.False(t, seen[privateResult.ID])

	// Editing the rule must not relabel a historical result or reveal a hidden account.
	legacy.TargetMode, legacy.AccountID = "account", &accountID
	legacy.TestDefinitionID, legacy.TestDefinitionIDs = &htmlID, []int64{htmlID}
	_, err = plans.Update(ctx, legacy)
	require.NoError(t, err)
	legacyResult, err = results.GetByID(ctx, legacyResultID)
	require.NoError(t, err)
	visible, err = results.ListVisible(ctx, 100, 4)
	require.NoError(t, err)
	for _, result := range visible {
		if result.ID == legacyResult.ID {
			require.Nil(t, result.AccountID)
			require.Equal(t, candyID, *result.TestDefinitionID)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "account_id")
		}
	}
	execSQL("INSERT INTO user_allowed_groups VALUES (100, 9)")
	visible, err = results.ListVisible(ctx, 100, 4)
	require.NoError(t, err)
	privateVisible := false
	for _, result := range visible {
		privateVisible = privateVisible || result.ID == privateResult.ID
	}
	require.True(t, privateVisible)

	// Retention keeps a successful history independently from failures and in-flight runs.
	createResult(htmlID, "failed", "model-a", "high", -1)
	require.NoError(t, results.PruneOldResults(ctx, plan.ID, 1))
	var successCount, runningCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_results WHERE plan_id=$1 AND status='success'`, plan.ID).Scan(&successCount))
	require.Equal(t, 5, successCount, "types, models, efforts and groups retain separate successes")
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_results WHERE plan_id=$1 AND status='running'`, plan.ID).Scan(&runningCount))
	require.Equal(t, 2, runningCount)
	// A definition referenced only by historical results also remains protected.
	plan.TestDefinitionIDs, plan.TestDefinitionID = []int64{htmlID}, &htmlID
	_, err = plans.Update(ctx, plan)
	require.NoError(t, err)
	require.Error(t, definitions.Delete(ctx, candyID))

	t.Run("secondary definition is protected during concurrent plan creation", func(t *testing.T) {
		definition, err := definitions.Create(ctx, &service.ScheduledTestDefinition{
			Key: "concurrent", Name: "Concurrent", Prompt: "Test", OutputKind: "text", Enabled: true,
		})
		require.NoError(t, err)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		var newPlanID int64
		require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans
			(name, group_id, test_definition_id, test_definition_ids, target_mode)
			VALUES ('Concurrent plan', 8, $1, $2, 'group') RETURNING id`, htmlID, pq.Array([]int64{htmlID, definition.ID})).Scan(&newPlanID))
		deleted := make(chan error, 1)
		go func() { deleted <- definitions.Delete(ctx, definition.ID) }()
		select {
		case deleteErr := <-deleted:
			t.Fatalf("definition deletion should wait for the uncommitted reference: %v", deleteErr)
		case <-time.After(50 * time.Millisecond):
		}
		require.NoError(t, tx.Commit())
		require.Error(t, <-deleted, "the concurrent delete must not leave a broken secondary reference")
		require.NoError(t, plans.Delete(ctx, newPlanID))
		require.NoError(t, definitions.Delete(ctx, definition.ID), "deleting a plan releases its references")
	})
}
