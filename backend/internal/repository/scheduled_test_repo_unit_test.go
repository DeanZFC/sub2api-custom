//go:build unit

package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestPlanRepositoryCreatePersistsName(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := &scheduledTestPlanRepository{db: db}
	accountID := int64(7)
	createdAt := time.Now()
	nextRun := createdAt.Add(time.Minute)
	mock.ExpectQuery(`SELECT EXISTS \(SELECT 1 FROM accounts WHERE id = \$1 AND deleted_at IS NULL\)`).
		WithArgs(accountID).
		WillReturnRows(sqlmock.NewRows([]string{"exists"}).AddRow(true))
	mock.ExpectQuery(`(?s)INSERT INTO scheduled_test_plans \(name, sort_order, account_id`).
		WithArgs("nightly candy", 0, accountID, nil, nil, "candy", "account", "model", "", "*/5 * * * *", true, 20, false, nextRun, nil, sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "sort_order", "account_id", "group_id", "test_definition_id", "test_type", "target_mode", "model_id", "reasoning_effort", "cron_expression", "enabled", "max_results", "auto_recover", "last_run_at", "next_run_at", "created_at", "updated_at", "test_definition_ids", "protection",
		}).AddRow(10, "nightly candy", 0, accountID, nil, nil, "candy", "account", "model", "", "*/5 * * * *", true, 20, false, nil, nextRun, createdAt, createdAt, "{}", "{}"))

	got, err := repo.Create(context.Background(), &service.ScheduledTestPlan{
		Name: "nightly candy", AccountID: &accountID, TargetMode: "account", TestType: "candy", ModelID: "model", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 20, NextRunAt: &nextRun,
	})
	require.NoError(t, err)
	require.Equal(t, "nightly candy", got.Name)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryListIncludesDisplayNames(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := &scheduledTestResultRepository{db: db}
	createdAt := time.Now()
	mock.ExpectQuery(`(?s)SELECT r\.id, r\.plan_id.*COALESCE\(a.name, ''\).*FROM scheduled_test_results r.*LEFT JOIN accounts a ON a.id = r.account_id.*ORDER BY r\.started_at DESC, r\.id DESC`).
		WithArgs(int64(10), 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "plan_name", "test_name", "test_order", "group_name", "plan_order", "target_mode", "status", "response_text", "output_kind", "output_html", "output_numeric", "account_id", "model_id", "reasoning_effort", "group_id", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "test_definition_id", "account_name",
		}).AddRow(1, 10, "nightly candy", "糖果数字测试", 0, "公开组", 12, "all_accounts", "success", "答案：29", "number", "", 29.0, 7, "model", "high", 3, "", 42, createdAt, createdAt, createdAt, 2, "Account Seven"))
	mock.ExpectQuery(`(?s)SELECT id, protection_decision.*FROM scheduled_test_results`).
		WithArgs(sqlmock.AnyArg()).
		WillReturnRows(sqlmock.NewRows([]string{"id", "protection_decision"}))

	results, err := repo.ListByPlanID(context.Background(), 10, 20)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "nightly candy", results[0].PlanName)
	require.Equal(t, "糖果数字测试", results[0].TestName)
	require.Equal(t, "公开组", results[0].GroupName)
	require.Equal(t, 12, results[0].PlanOrder)
	require.Equal(t, "high", results[0].ReasoningEffort)
	require.Equal(t, "Account Seven", results[0].AccountName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryPersistsReasoningEffort(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := &scheduledTestResultRepository{db: db}
	now := time.Now()
	accountID, groupID := int64(7), int64(3)
	input := &service.ScheduledTestResult{
		PlanID: 10, Status: "running", OutputKind: "text", AccountID: &accountID,
		GroupID: &groupID, ModelID: "gpt-6-astra", ReasoningEffort: "ultra", StartedAt: now, FinishedAt: now,
	}
	mock.ExpectQuery(`(?s)INSERT INTO scheduled_test_results .*reasoning_effort.*RETURNING .*reasoning_effort`).
		WithArgs(int64(10), "running", "", "text", "", nil, accountID, "gpt-6-astra", "ultra", groupID, "", int64(0), now, now, nil, "", "").
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "status", "response_text", "output_kind", "output_html", "output_numeric", "account_id", "model_id", "reasoning_effort", "group_id", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "test_definition_id", "target_mode",
		}).AddRow(1, 10, "running", "", "text", "", nil, accountID, "gpt-6-astra", "ultra", groupID, "", 0, now, now, now, nil, ""))
	created, err := repo.Create(context.Background(), input)
	require.NoError(t, err)
	require.Equal(t, "ultra", created.ReasoningEffort)

	created.Status = "success"
	created.ResponseText = "done"
	mock.ExpectExec(`(?s)UPDATE scheduled_test_results .*reasoning_effort = \$9`).
		WithArgs(int64(1), "success", "done", "text", "", nil, accountID, "gpt-6-astra", "ultra", groupID, "", int64(0), now, now).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Update(context.Background(), created))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryVisibleUsesResultReasoningEffort(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := &scheduledTestResultRepository{db: db}
	now := time.Now()
	mock.ExpectQuery(`(?s)WITH projected_results AS .*r.target_mode IN \('account', 'all_accounts'\).*visible_account_id.*result_target_key.*FROM scheduled_test_results r.*success_state AS.*PARTITION BY group_id, result_target_key, test_definition_id, model_id, reasoning_effort.*ranked_results AS.*WHERE status IN \('success', 'passed'\).*NOT has_success.*WHERE history_rank <= \$2.*ORDER BY vr\.started_at DESC, vr\.id DESC`).
		WithArgs(int64(5), 3).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "plan_name", "test_name", "test_order", "group_name", "plan_order", "target_mode", "status", "response_text", "output_kind", "output_html", "output_numeric", "account_id", "model_id", "reasoning_effort", "group_id", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "test_definition_id",
		}).AddRow(1, 10, "nightly candy", "糖果数字测试", 0, "公开组", 12, "all_accounts", "success", "29", "number", "", 29.0, 7, "model", "medium", 3, "", 42, now, now, now, 2))
	results, err := repo.ListVisible(context.Background(), 5, 20)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "medium", results[0].ReasoningEffort)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := &scheduledTestResultRepository{db: db}
	mock.ExpectQuery(`SELECT plan_id,account_id FROM scheduled_test_results WHERE id=\$1`).
		WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"plan_id", "account_id"}).AddRow(8, nil))
	mock.ExpectBegin()
	mock.ExpectQuery(`SELECT enabled, protection FROM scheduled_test_plans WHERE id=\$1 FOR NO KEY UPDATE`).
		WithArgs(int64(8)).WillReturnRows(sqlmock.NewRows([]string{"enabled", "protection"}).AddRow(true, "{}"))
	mock.ExpectExec(`DELETE FROM scheduled_test_results WHERE id = \$1`).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, repo.Delete(context.Background(), 42))
	require.NoError(t, mock.ExpectationsWereMet())
}
