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
	mock.ExpectQuery(`(?s)INSERT INTO scheduled_test_plans \(name, account_id`).
		WithArgs("nightly candy", accountID, nil, nil, "candy", "model", "", "*/5 * * * *", true, 20, false, nextRun).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "name", "account_id", "group_id", "test_definition_id", "test_type", "model_id", "reasoning_effort", "cron_expression", "enabled", "max_results", "auto_recover", "last_run_at", "next_run_at", "created_at", "updated_at",
		}).AddRow(10, "nightly candy", accountID, nil, nil, "candy", "model", "", "*/5 * * * *", true, 20, false, nil, nextRun, createdAt, createdAt))

	got, err := repo.Create(context.Background(), &service.ScheduledTestPlan{
		Name: "nightly candy", AccountID: &accountID, TestType: "candy", ModelID: "model", CronExpression: "*/5 * * * *", Enabled: true, MaxResults: 20, NextRunAt: &nextRun,
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
	mock.ExpectQuery(`(?s)SELECT r\.id, r\.plan_id.*FROM scheduled_test_results r`).
		WithArgs(int64(10), 20).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "plan_name", "test_name", "group_name", "status", "response_text", "output_kind", "output_html", "output_numeric", "account_id", "model_id", "group_id", "error_message", "latency_ms", "started_at", "finished_at", "created_at",
		}).AddRow(1, 10, "nightly candy", "糖果数字测试", "公开组", "success", "答案：29", "number", "", 29.0, 7, "model", 3, "", 42, createdAt, createdAt, createdAt))

	results, err := repo.ListByPlanID(context.Background(), 10, 20)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, "nightly candy", results[0].PlanName)
	require.Equal(t, "糖果数字测试", results[0].TestName)
	require.Equal(t, "公开组", results[0].GroupName)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryDelete(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()

	repo := &scheduledTestResultRepository{db: db}
	mock.ExpectExec(`DELETE FROM scheduled_test_results WHERE id = \$1`).
		WithArgs(int64(42)).
		WillReturnResult(sqlmock.NewResult(0, 1))
	require.NoError(t, repo.Delete(context.Background(), 42))
	require.NoError(t, mock.ExpectationsWereMet())
}
