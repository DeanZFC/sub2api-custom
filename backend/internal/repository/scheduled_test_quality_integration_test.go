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
	execSQL(`CREATE TABLE users (id BIGINT PRIMARY KEY);
		CREATE TABLE accounts (id BIGINT PRIMARY KEY, name TEXT, status TEXT NOT NULL DEFAULT 'active', schedulable BOOLEAN NOT NULL DEFAULT true, deleted_at TIMESTAMPTZ);
		CREATE TABLE groups (id BIGINT PRIMARY KEY, name TEXT, status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, is_exclusive BOOLEAN DEFAULT false, subscription_type TEXT DEFAULT 'standard');
		CREATE TABLE account_groups (account_id BIGINT, group_id BIGINT);
		CREATE TABLE user_allowed_groups (user_id BIGINT, group_id BIGINT);
		CREATE TABLE user_subscriptions (user_id BIGINT, group_id BIGINT, deleted_at TIMESTAMPTZ, status TEXT, starts_at TIMESTAMPTZ, expires_at TIMESTAMPTZ);
		INSERT INTO accounts (id, name) VALUES (62, 'Private account alpha'), (63, 'Private account beta');
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
	applyMigration("257_scheduled_test_hourly_statistics.sql")
	applyMigration("257_scheduled_test_hourly_statistics.sql")
	applyMigration("258_scheduled_test_protection.sql")
	applyMigration("259_scheduled_test_outcome_actions.sql")
	applyMigration("260_scheduled_test_model_check.sql")
	applyMigration("261_scheduled_test_admin_review.sql")
	applyMigration("262_scheduled_test_execution_snapshot.sql")
	applyMigration("262_scheduled_test_execution_snapshot.sql")
	var statisticsCount int
	require.NoError(t, db.QueryRowContext(ctx, `SELECT count(*) FROM scheduled_test_definitions WHERE key='hourly_stats' AND output_kind='statistics' AND prompt='' AND enabled AND sort_order=2`).Scan(&statisticsCount))
	require.Equal(t, 1, statisticsCount, "the local statistics definition is seeded idempotently")
	plans := NewScheduledTestPlanRepository(db)
	results := &scheduledTestResultRepository{db: db}
	definitions := NewScheduledTestDefinitionRepository(db)
	legacy, err := plans.GetByID(ctx, legacyPlanID)
	require.NoError(t, err)
	require.Equal(t, []int64{candyID}, legacy.TestDefinitionIDs)
	legacyResult, err := results.GetByID(ctx, legacyResultID)
	require.NoError(t, err)
	require.Equal(t, candyID, *legacyResult.TestDefinitionID)
	require.Equal(t, "group", legacyResult.TargetMode)
	require.Equal(t, "Private account alpha", legacyResult.AccountName)
	execSQL("UPDATE accounts SET deleted_at=NOW() WHERE id=62")
	deletedAccountResult, err := results.GetByID(ctx, legacyResultID)
	require.NoError(t, err)
	require.Equal(t, "Private account alpha", deletedAccountResult.AccountName, "admin history must keep soft-deleted account names")
	deletedAccountHistory, err := results.ListByPlanID(ctx, legacyPlanID, 3)
	require.NoError(t, err)
	require.Len(t, deletedAccountHistory, 1)
	require.Equal(t, "Private account alpha", deletedAccountHistory[0].AccountName)
	execSQL("UPDATE accounts SET deleted_at=NULL WHERE id=62")

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
		require.Empty(t, result.AccountName, "public previews must not load account names")
		if result.ID == legacyResultID {
			require.Nil(t, result.AccountID, "group results must not expose the executing account")
			continue
		}
		seriesCounts[fmt.Sprintf("%d/%s/%s", *result.TestDefinitionID, result.ModelID, result.ReasoningEffort)]++
	}
	require.Equal(t, 3, seriesCounts[fmt.Sprintf("%d/model-a/high", candyID)], "the public preview caps each series at three results")
	require.Equal(t, 3, seriesCounts[fmt.Sprintf("%d/model-a/high", htmlID)])
	require.Equal(t, 1, seriesCounts[fmt.Sprintf("%d/model-a/medium", candyID)])
	require.Equal(t, 1, seriesCounts[fmt.Sprintf("%d/model-b/high", candyID)])
	require.True(t, seen[initialRunning.ID])
	require.False(t, seen[running.ID], "progress must not displace a previous success")
	require.False(t, seen[failed.ID])
	require.False(t, seen[privateResult.ID])
	publicJSON, err := json.Marshal(visible)
	require.NoError(t, err)
	require.NotContains(t, string(publicJSON), "account_name")
	require.NotContains(t, string(publicJSON), "Private account alpha")

	t.Run("public history is authorized and paginates one successful series", func(t *testing.T) {
		var anchor *service.ScheduledTestResult
		for _, result := range visible {
			if result.TargetMode == "all_accounts" && *result.TestDefinitionID == candyID && result.ModelID == "model-a" && result.ReasoningEffort == "high" {
				anchor = result
				break
			}
		}
		require.NotNil(t, anchor)
		first, err := results.ListVisibleHistory(ctx, 100, anchor.ID, 0, 2)
		require.NoError(t, err)
		require.Len(t, first, 2)
		second, err := results.ListVisibleHistory(ctx, 100, anchor.ID, first[1].ID, 10)
		require.NoError(t, err)
		require.Len(t, second, 4)
		all := append(first, second...)
		for i, result := range all {
			require.Equal(t, "success", result.Status)
			require.Empty(t, result.AccountName, "public history must not load account names")
			require.Equal(t, candyID, *result.TestDefinitionID)
			require.Equal(t, "model-a", result.ModelID)
			require.Equal(t, "high", result.ReasoningEffort)
			require.Equal(t, accountID, *result.AccountID)
			if i > 0 {
				require.True(t, result.StartedAt.Before(all[i-1].StartedAt))
			}
		}
		historyJSON, err := json.Marshal(all)
		require.NoError(t, err)
		require.NotContains(t, string(historyJSON), "account_name")
		require.NotContains(t, string(historyJSON), "Private account alpha")
		last, err := results.ListVisibleHistory(ctx, 100, anchor.ID, all[len(all)-1].ID, 10)
		require.NoError(t, err)
		require.Empty(t, last)
		for _, hiddenID := range []int64{privateResult.ID, failed.ID, running.ID, initialRunning.ID, 999999} {
			_, err := results.ListVisibleHistory(ctx, 100, hiddenID, 0, 20)
			require.ErrorIs(t, err, sql.ErrNoRows)
			_, err = results.ListVisibleHistory(ctx, 100, anchor.ID, hiddenID, 20)
			require.ErrorIs(t, err, sql.ErrNoRows)
		}
		_, err = results.ListVisibleHistory(ctx, 100, anchor.ID, legacyResultID, 20)
		require.ErrorIs(t, err, sql.ErrNoRows, "a visible cursor from another series must be rejected")
		groupHistory, err := results.ListVisibleHistory(ctx, 100, legacyResultID, 0, 20)
		require.NoError(t, err)
		require.Len(t, groupHistory, 1)
		encoded, err := json.Marshal(groupHistory)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "account_id")
		require.NotContains(t, string(encoded), "account_name")
		require.NotContains(t, string(encoded), "credentials")

		// A manually retried result keeps its ID but becomes the newest result.
		oldest := all[len(all)-1]
		oldest.StartedAt = started.Add(time.Minute)
		require.NoError(t, results.Update(ctx, oldest))
		reordered, err := results.ListVisibleHistory(ctx, 100, anchor.ID, 0, 2)
		require.NoError(t, err)
		require.Equal(t, oldest.ID, reordered[0].ID)
		oldest.StartedAt = started.Add(-6 * time.Minute)
		require.NoError(t, results.Update(ctx, oldest))
		tied := all[len(all)-2]
		tiedStartedAt := tied.StartedAt
		tied.StartedAt = oldest.StartedAt
		require.NoError(t, results.Update(ctx, tied))
		tiePage, err := results.ListVisibleHistory(ctx, 100, anchor.ID, all[len(all)-3].ID, 1)
		require.NoError(t, err)
		require.Len(t, tiePage, 1)
		require.Equal(t, oldest.ID, tiePage[0].ID, "equal timestamps sort by descending ID")
		tieNext, err := results.ListVisibleHistory(ctx, 100, anchor.ID, oldest.ID, 1)
		require.NoError(t, err)
		require.Len(t, tieNext, 1)
		require.Equal(t, tied.ID, tieNext[0].ID)
		tied.StartedAt = tiedStartedAt
		require.NoError(t, results.Update(ctx, tied))
	})

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
	privateHistory, err := results.ListVisibleHistory(ctx, 100, privateResult.ID, 0, 20)
	require.NoError(t, err)
	require.Len(t, privateHistory, 1)
	execSQL("DELETE FROM user_allowed_groups WHERE user_id=100 AND group_id=9")
	_, err = results.ListVisibleHistory(ctx, 100, privateResult.ID, 0, 20)
	require.ErrorIs(t, err, sql.ErrNoRows, "history rechecks permissions after access is revoked")

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

	t.Run("admin history limit preserves every account and test type", func(t *testing.T) {
		multiPlan, err := plans.Create(ctx, &service.ScheduledTestPlan{
			Name: "History limit", GroupID: &groupID, TestDefinitionID: &htmlID,
			TestDefinitionIDs: []int64{htmlID, candyID}, TestType: "quality", TargetMode: "all_accounts",
			ModelID: "model-a", ReasoningEffort: "high", CronExpression: "0 * * * *", Enabled: true, MaxResults: 1,
		})
		require.NoError(t, err)
		// The second type finishes later, as it does during sequential execution.
		// A plan-wide LIMIT would hide every result of the first type.
		latestBySeries := make(map[[2]int64]int64)
		sequence := 0
		addResult := func(accountID, definitionID int64, status, model, effort string) int64 {
			t.Helper()
			sequence++
			runAt := started.Add(time.Duration(sequence) * time.Second)
			result, err := results.Create(ctx, &service.ScheduledTestResult{
				PlanID: multiPlan.ID, TestDefinitionID: &definitionID, TargetMode: "all_accounts",
				GroupID: &groupID, AccountID: &accountID, Status: status, ModelID: model,
				ReasoningEffort: effort, OutputKind: "text", ResponseText: "Output",
				StartedAt: runAt, FinishedAt: runAt,
			})
			require.NoError(t, err)
			return result.ID
		}
		for _, definitionID := range []int64{htmlID, candyID} {
			for _, accountID := range []int64{62, 63} {
				addResult(accountID, definitionID, "success", "model-a", "high")
				latestBySeries[[2]int64{definitionID, accountID}] = addResult(accountID, definitionID, "passed", "model-a", "high")
			}
		}
		// A later failure, in-flight run, model, or effort replaces the previous
		// row for the same account/type in the administrator's latest view.
		addResult(62, htmlID, "failed", "model-a", "high")
		addResult(62, htmlID, "failed", "model-a", "high")
		addResult(62, htmlID, "running", "model-a", "high")
		addResult(62, htmlID, "pending", "model-a", "high")
		addResult(62, htmlID, "success", "model-b", "high")
		latestBySeries[[2]int64{htmlID, 62}] = addResult(62, htmlID, "success", "model-a", "medium")

		history, err := results.ListByPlanID(ctx, multiPlan.ID, 1)
		require.NoError(t, err)
		var actualIDs []int64
		for i, result := range history {
			require.NotEmpty(t, result.AccountName, "admin history includes the tested account's name")
			actualIDs = append(actualIDs, result.ID)
			require.Equal(t, multiPlan.ID, result.PlanID)
			if i > 0 {
				require.False(t, result.StartedAt.After(history[i-1].StartedAt))
			}
		}
		var expectedIDs []int64
		for _, id := range latestBySeries {
			expectedIDs = append(expectedIDs, id)
		}
		require.ElementsMatch(t, expectedIDs, actualIDs)
	})

	t.Run("latest execution excludes removed accounts and types and retains queued targets", func(t *testing.T) {
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{Name: "Snapshot", GroupID: &groupID, TestDefinitionID: &htmlID, TestDefinitionIDs: []int64{htmlID, candyID}, TargetMode: "all_accounts", ModelID: "model", CronExpression: "* * * * *", MaxResults: 10})
		require.NoError(t, err)
		input := func(accountID, definitionID int64, at time.Time) *service.ScheduledTestResult {
			return &service.ScheduledTestResult{PlanID: p.ID, AccountID: &accountID, GroupID: &groupID, TestDefinitionID: &definitionID, TargetMode: "all_accounts", ModelID: "model", Status: "pending", OutputKind: "text", StartedAt: at, FinishedAt: at}
		}
		older, err := results.BeginRun(ctx, p.ID, "older", []*service.ScheduledTestResult{input(62, htmlID, started), input(63, candyID, started)})
		require.NoError(t, err)
		for _, result := range older {
			result.Status = "success"
			require.NoError(t, results.Update(ctx, result))
		}
		newer, err := results.BeginRun(ctx, p.ID, "newer", []*service.ScheduledTestResult{input(62, htmlID, started.Add(time.Minute))})
		require.NoError(t, err)
		latest, err := results.ListByPlanID(ctx, p.ID, 1)
		require.NoError(t, err)
		require.Len(t, latest, 1)
		require.Equal(t, newer[0].ID, latest[0].ID)
		require.Equal(t, "pending", latest[0].Status)
		history, err := results.ListByPlanID(ctx, p.ID, 10)
		require.NoError(t, err)
		require.Len(t, history, 3)
		_, err = results.BeginRun(ctx, p.ID, "broken", []*service.ScheduledTestResult{input(62, htmlID, started), {PlanID: p.ID + 100}})
		require.Error(t, err)
		latest, err = results.ListByPlanID(ctx, p.ID, 1)
		require.NoError(t, err)
		require.Len(t, latest, 1)
		require.Equal(t, newer[0].ID, latest[0].ID, "failed snapshot publication rolls back both rows and the latest-run pointer")
	})

	t.Run("group-mode retention keeps every actual account and type", func(t *testing.T) {
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{Name: "Group retention", GroupID: &groupID, TestDefinitionID: &htmlID, TestDefinitionIDs: []int64{htmlID, candyID}, TargetMode: "group", ModelID: "model", CronExpression: "* * * * *", MaxResults: 1})
		require.NoError(t, err)
		for _, id := range []int64{62, 63} {
			for _, definition := range []int64{htmlID, candyID} {
				for round := 0; round < 2; round++ {
					at := started.Add(time.Duration(round) * time.Minute)
					_, err = results.Create(ctx, &service.ScheduledTestResult{PlanID: p.ID, AccountID: &id, GroupID: &groupID, TestDefinitionID: &definition, TargetMode: "group", Status: "success", OutputKind: "text", StartedAt: at, FinishedAt: at})
					require.NoError(t, err)
				}
			}
		}
		require.NoError(t, results.PruneOldResults(ctx, p.ID, 1))
		latest, err := results.ListByPlanID(ctx, p.ID, 1)
		require.NoError(t, err)
		require.Len(t, latest, 4, "one result per real account/type, even when public display hides account IDs")
	})

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

	t.Run("statistics storage survives public projection without exposing raw JSON", func(t *testing.T) {
		definition, err := definitions.GetByKey(ctx, "hourly_stats")
		require.NoError(t, err)
		statsPlan, err := plans.Create(ctx, &service.ScheduledTestPlan{
			Name: "Local statistics", GroupID: &groupID, TestDefinitionID: &definition.ID,
			TestDefinitionIDs: []int64{definition.ID}, TestType: "quality", TargetMode: "group",
			ModelID: "model-a", CronExpression: "0 * * * *", Enabled: true, MaxResults: 5,
		})
		require.NoError(t, err)
		svc := service.NewScheduledTestService(plans, results)
		pending, err := svc.StartResult(ctx, statsPlan.ID, &service.ScheduledTestResult{
			TestDefinitionID: &definition.ID, GroupID: &groupID, TargetMode: "group",
			ModelID: "model-a", OutputKind: "statistics", StartedAt: started,
		})
		require.NoError(t, err)
		snapshot := &service.ScheduledTestStatistics{
			WindowStart: started.Add(-time.Hour), WindowEnd: started, TotalRequests: 4, SuccessRequests: 3, FailedRequests: 1,
			RecentRequests: []service.ScheduledTestRecentRequest{
				{Success: false, CreatedAt: started.Add(-time.Minute)},
				{Success: true, CreatedAt: started.Add(-2 * time.Minute)},
			},
		}
		rate := 0.75
		snapshot.SuccessRate = &rate
		encoded, err := json.Marshal(snapshot)
		require.NoError(t, err)
		pending.Status, pending.ResponseText, pending.FinishedAt = "success", string(encoded), started
		pending.OutputStatistics = snapshot
		require.NoError(t, svc.CompleteResult(ctx, 5, pending))
		stored, err := results.GetByID(ctx, pending.ID)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), stored.ResponseText)
		visible, err := svc.ListVisibleResults(ctx, 100, 5)
		require.NoError(t, err)
		found := false
		for _, result := range visible {
			if result.ID != pending.ID {
				continue
			}
			found = true
			require.Empty(t, result.ResponseText)
			require.Nil(t, result.AccountID)
			require.Empty(t, result.AccountName)
			require.Empty(t, result.ReasoningEffort)
			require.Equal(t, snapshot, result.OutputStatistics)
		}
		require.True(t, found)
		stored, err = results.GetByID(ctx, pending.ID)
		require.NoError(t, err)
		require.JSONEq(t, string(encoded), stored.ResponseText, "read normalization must never overwrite stored statistics")
	})

	t.Run("public results follow current account availability while admin history remains", func(t *testing.T) {
		const unavailableID, availableID = int64(70), int64(71)
		execSQL(`INSERT INTO accounts (id,name) VALUES (70,'Visibility changing account'),(71,'Visibility control account');
INSERT INTO account_groups VALUES (70,8),(71,8)`)
		definition, err := definitions.GetByKey(ctx, "hourly_stats")
		require.NoError(t, err)
		statsPlan, err := plans.Create(ctx, &service.ScheduledTestPlan{
			Name: "Availability independent statistics", GroupID: &groupID,
			TestDefinitionID: &definition.ID, TestDefinitionIDs: []int64{definition.ID},
			TargetMode: "group", TestType: "quality", ModelID: "visibility-stats", CronExpression: "* * * * *",
		})
		require.NoError(t, err)
		groupStatistics, err := results.Create(ctx, &service.ScheduledTestResult{
			PlanID: statsPlan.ID, TestDefinitionID: &definition.ID, GroupID: &groupID,
			TargetMode: "group", Status: "success", OutputKind: "statistics", ModelID: "visibility-stats",
			StartedAt: started, FinishedAt: started,
		})
		require.NoError(t, err)
		containsID := func(rows []*service.ScheduledTestResult, id int64) bool {
			for _, row := range rows {
				if row.ID == id {
					return true
				}
			}
			return false
		}
		for _, mode := range []string{"account", "all_accounts", "group"} {
			t.Run(mode, func(t *testing.T) {
				plan := &service.ScheduledTestPlan{
					Name: "Availability " + mode, GroupID: &groupID, TestDefinitionID: &candyID,
					TestDefinitionIDs: []int64{candyID}, TargetMode: mode, TestType: "quality",
					ModelID: "visibility-" + mode, CronExpression: "* * * * *",
				}
				accountID := unavailableID
				if mode == "account" {
					plan.AccountID = &accountID
				}
				plan, err = plans.Create(ctx, plan)
				require.NoError(t, err)
				create := func(id *int64, age time.Duration) *service.ScheduledTestResult {
					t.Helper()
					result, err := results.Create(ctx, &service.ScheduledTestResult{
						PlanID: plan.ID, TestDefinitionID: &candyID, GroupID: &groupID, AccountID: id,
						TargetMode: mode, Status: "success", OutputKind: "number", ResponseText: "29",
						ModelID: plan.ModelID, StartedAt: started.Add(-age), FinishedAt: started,
					})
					require.NoError(t, err)
					return result
				}
				older := create(&accountID, 3*time.Minute)
				latest := create(&accountID, time.Minute)
				var control, orphan *service.ScheduledTestResult
				if mode == "group" {
					controlID := availableID
					control = create(&controlID, 30*time.Second)
				} else {
					// Deleted-account foreign keys may leave an orphaned account
					// result. A public group entitlement must not make it visible.
					orphan = create(nil, 0)
				}
				initial, err := results.ListVisible(ctx, 100, 3)
				require.NoError(t, err)
				require.True(t, containsID(initial, latest.ID))
				if orphan != nil {
					require.False(t, containsID(initial, orphan.ID))
					_, err := results.ListVisibleHistory(ctx, 100, orphan.ID, 0, 20)
					require.ErrorIs(t, err, sql.ErrNoRows)
				}

				for _, state := range []struct {
					name        string
					status      string
					schedulable bool
					deleted     any
				}{
					{name: "error", status: "error", schedulable: true},
					{name: "inactive", status: "inactive", schedulable: true},
					{name: "unschedulable", status: "active", schedulable: false},
					{name: "soft deleted", status: "active", schedulable: true, deleted: started},
				} {
					t.Run(state.name, func(t *testing.T) {
						execSQL("UPDATE accounts SET status=$2,schedulable=$3,deleted_at=$4 WHERE id=$1", unavailableID, state.status, state.schedulable, state.deleted)
						public, err := results.ListVisible(ctx, 100, 3)
						require.NoError(t, err)
						require.False(t, containsID(public, older.ID))
						require.False(t, containsID(public, latest.ID))
						require.True(t, containsID(public, groupStatistics.ID), "a group-only statistics snapshot does not depend on an executing account")
						for _, beforeID := range []int64{0, older.ID} {
							_, err = results.ListVisibleHistory(ctx, 100, latest.ID, beforeID, 20)
							require.ErrorIs(t, err, sql.ErrNoRows, "historical anchors must recheck the current account state")
						}
						if control != nil {
							require.True(t, containsID(public, control.ID), "another active group-test account stays visible")
							history, err := results.ListVisibleHistory(ctx, 100, control.ID, 0, 20)
							require.NoError(t, err)
							require.Len(t, history, 1, "a group series must exclude unavailable underlying accounts even though their IDs are hidden")
							require.Equal(t, control.ID, history[0].ID)
							require.Nil(t, history[0].AccountID)
							_, err = results.ListVisibleHistory(ctx, 100, control.ID, older.ID, 20)
							require.ErrorIs(t, err, sql.ErrNoRows, "a hidden cursor in the same group series cannot bypass availability checks")
						}
						adminHistory, err := results.ListByPlanID(ctx, plan.ID, 50)
						require.NoError(t, err)
						require.True(t, containsID(adminHistory, older.ID))
						require.True(t, containsID(adminHistory, latest.ID))
						adminResult, err := results.GetByID(ctx, latest.ID)
						require.NoError(t, err)
						require.Equal(t, "Visibility changing account", adminResult.AccountName)
						if orphan != nil {
							require.True(t, containsID(adminHistory, orphan.ID), "administrators retain orphaned-result diagnostics")
						}

						execSQL("UPDATE accounts SET status='active',schedulable=true,deleted_at=NULL WHERE id=$1", unavailableID)
						restored, err := results.ListVisible(ctx, 100, 3)
						require.NoError(t, err)
						require.True(t, containsID(restored, latest.ID), "eligible accounts become visible without rewriting historical results")
						history, err := results.ListVisibleHistory(ctx, 100, latest.ID, 0, 20)
						require.NoError(t, err)
						require.True(t, containsID(history, older.ID))
						require.True(t, containsID(history, latest.ID))
					})
				}
			})
		}
		execSQL("UPDATE accounts SET status='error',schedulable=false WHERE id IN (70,71)")
		public, err := results.ListVisible(ctx, 100, 3)
		require.NoError(t, err)
		require.True(t, containsID(public, groupStatistics.ID))
		history, err := results.ListVisibleHistory(ctx, 100, groupStatistics.ID, 0, 20)
		require.NoError(t, err)
		require.Len(t, history, 1)
		require.Nil(t, history[0].AccountID)

		t.Run("public result follows moved account group", func(t *testing.T) {
			const movedAccountID = int64(72)
			movedPlan, err := plans.Create(ctx, &service.ScheduledTestPlan{
				Name: "Dynamic account group", GroupID: &groupID, TestDefinitionID: &candyID,
				TestDefinitionIDs: []int64{candyID}, TargetMode: "all_accounts", TestType: "quality",
				ModelID: "dynamic-group", CronExpression: "* * * * *",
			})
			require.NoError(t, err)
			execSQL("INSERT INTO accounts (id,name) VALUES ($1,'Moved account')", movedAccountID)
			execSQL("INSERT INTO account_groups VALUES ($1,$2)", movedAccountID, groupID)
			movedAccount := movedAccountID
			result, err := results.Create(ctx, &service.ScheduledTestResult{
				PlanID: movedPlan.ID, TestDefinitionID: &candyID, GroupID: &groupID,
				AccountID: &movedAccount, TargetMode: "all_accounts", Status: "success",
				OutputKind: "number", ResponseText: "29", ModelID: movedPlan.ModelID,
				StartedAt: started, FinishedAt: started,
			})
			require.NoError(t, err)

			visible, err := results.ListVisible(ctx, 100, 3)
			require.NoError(t, err)
			var projected *service.ScheduledTestResult
			for _, row := range visible {
				if row.ID == result.ID {
					projected = row
					break
				}
			}
			require.NotNil(t, projected)
			require.Equal(t, groupID, *projected.GroupID)
			require.Equal(t, "Public group", projected.GroupName)

			// A protection action can remove the source group and add a new one.
			// Public results must immediately follow that current membership while
			// retaining the original source group on the stored/admin row.
			execSQL("DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2", movedAccountID, groupID)
			execSQL("INSERT INTO account_groups VALUES ($1,$2)", movedAccountID, privateGroupID)
			execSQL("INSERT INTO user_allowed_groups VALUES (100,$1) ON CONFLICT DO NOTHING", privateGroupID)
			visible, err = results.ListVisible(ctx, 100, 3)
			require.NoError(t, err)
			projected = nil
			for _, row := range visible {
				if row.ID == result.ID {
					projected = row
					break
				}
			}
			require.NotNil(t, projected)
			require.Equal(t, privateGroupID, *projected.GroupID)
			require.Equal(t, "Private group", projected.GroupName)
			history, err := results.ListVisibleHistory(ctx, 100, result.ID, 0, 20)
			require.NoError(t, err)
			require.Len(t, history, 1)
			require.Equal(t, privateGroupID, *history[0].GroupID)

			// Once the account leaves all groups, neither latest nor history may
			// fall back to the stale source group and bypass entitlement checks.
			execSQL("DELETE FROM account_groups WHERE account_id=$1", movedAccountID)
			visible, err = results.ListVisible(ctx, 100, 3)
			require.NoError(t, err)
			for _, row := range visible {
				require.NotEqual(t, result.ID, row.ID)
			}
			_, err = results.ListVisibleHistory(ctx, 100, result.ID, 0, 20)
			require.ErrorIs(t, err, sql.ErrNoRows)
		})

	})
}
