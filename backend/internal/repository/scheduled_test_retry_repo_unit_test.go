//go:build unit

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestResultRepositoryGetByIDLoadsActualTestedAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	now := time.Now()
	mock.ExpectQuery(`(?s)SELECT r.id, r.plan_id, p.name,.*r.account_id,.*WHERE r.id = \$1`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "plan_name", "test_name", "group_name", "target_mode", "status", "response_text", "output_kind", "output_html", "output_numeric", "account_id", "model_id", "reasoning_effort", "group_id", "error_message", "latency_ms", "started_at", "finished_at", "created_at",
		}).AddRow(9, 3, "group test", "Pelican", "Group", "all_accounts", "failed", "", "html", "", nil, 12, "gpt-6-astra", "high", 7, "upstream error", 20, now, now, now))
	result, err := repo.GetByID(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.PlanID)
	require.NotNil(t, result.AccountID)
	require.Equal(t, int64(12), *result.AccountID)
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "all_accounts", result.TargetMode)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryGetByIDMissing(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	mock.ExpectQuery(`(?s)FROM scheduled_test_results r.*WHERE r.id = \$1`).WithArgs(int64(9)).WillReturnError(sql.ErrNoRows)
	_, err = repo.GetByID(context.Background(), 9)
	require.ErrorIs(t, err, sql.ErrNoRows)
	require.NoError(t, mock.ExpectationsWereMet())
}
