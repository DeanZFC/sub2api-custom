//go:build unit

package repository

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestResultRepositoryGetByIDLoadsActualTestedAccount(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	now := time.Now()
	mock.ExpectQuery(`(?s)SELECT r.id, r.plan_id, p.name,.*r.account_id,.*COALESCE\(a.name, ''\).*LEFT JOIN accounts a ON a.id = r.account_id.*WHERE r.id = \$1`).
		WithArgs(int64(9)).
		WillReturnRows(sqlmock.NewRows([]string{
			"id", "plan_id", "plan_name", "test_name", "test_order", "group_name", "plan_order", "target_mode", "status", "response_text", "output_kind", "output_html", "output_numeric", "account_id", "model_id", "reasoning_effort", "group_id", "error_message", "latency_ms", "started_at", "finished_at", "created_at", "test_definition_id", "account_name",
		}).AddRow(9, 3, "group test", "Pelican", 0, "Group", 7, "all_accounts", "failed", "", "html", "", nil, 12, "gpt-6-astra", "high", 7, "upstream error", 20, now, now, now, 1, "Private Account Twelve"))
	result, err := repo.GetByID(context.Background(), 9)
	require.NoError(t, err)
	require.Equal(t, int64(3), result.PlanID)
	require.NotNil(t, result.AccountID)
	require.Equal(t, int64(12), *result.AccountID)
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "all_accounts", result.TargetMode)
	require.Equal(t, "Private Account Twelve", result.AccountName)
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

func TestScheduledTestResultRepositoryRestartFailedUpdatesExistingRow(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	accountID, groupID := int64(12), int64(7)
	started, finished := time.Now(), time.Now().Add(time.Second)
	running := &service.ScheduledTestResult{
		ID: 9, PlanID: 3, AccountID: &accountID, GroupID: &groupID,
		Status: "running", OutputKind: "html", ModelID: "gpt-6-astra",
		ReasoningEffort: "high", StartedAt: started, FinishedAt: finished,
	}
	mock.ExpectExec(`(?s)UPDATE scheduled_test_results.*SET status = 'running'.*response_text = ''.*output_numeric = NULL.*WHERE id = \$1 AND plan_id = \$8 AND account_id = \$9 AND status = 'failed'`).
		WithArgs(int64(9), "html", "gpt-6-astra", "high", int64(7), started, finished, int64(3), int64(12), nil, "").
		WillReturnResult(sqlmock.NewResult(0, 1))

	require.NoError(t, repo.RestartFailed(context.Background(), running))
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryRestartFailedCASFailure(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	accountID := int64(12)
	running := &service.ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, StartedAt: time.Now(), FinishedAt: time.Now()}
	mock.ExpectExec(`(?s)UPDATE scheduled_test_results.*WHERE id = \$1 AND plan_id = \$8 AND account_id = \$9 AND status = 'failed'`).
		WithArgs(int64(9), "", "", "", nil, running.StartedAt, running.FinishedAt, int64(3), int64(12), nil, "").
		WillReturnResult(sqlmock.NewResult(0, 0))

	require.ErrorIs(t, repo.RestartFailed(context.Background(), running), service.ErrScheduledTestResultNotFailed)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryRestartFailedValidatesOwnershipIDs(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	accountID := int64(12)
	for _, result := range []*service.ScheduledTestResult{
		nil,
		{PlanID: 3, AccountID: &accountID},
		{ID: 9, AccountID: &accountID},
		{ID: 9, PlanID: 3},
		{ID: 9, PlanID: 3, AccountID: retryPtrInt64(0)},
	} {
		err := repo.RestartFailed(context.Background(), result)
		require.Error(t, err)
	}
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestResultRepositoryRestartFailedReturnsDatabaseError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &scheduledTestResultRepository{db: db}
	accountID := int64(12)
	running := &service.ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, StartedAt: time.Now(), FinishedAt: time.Now()}
	dbErr := errors.New("database unavailable")
	mock.ExpectExec(`(?s)UPDATE scheduled_test_results.*WHERE id = \$1 AND plan_id = \$8 AND account_id = \$9 AND status = 'failed'`).
		WithArgs(int64(9), "", "", "", nil, running.StartedAt, running.FinishedAt, int64(3), int64(12), nil, "").
		WillReturnError(dbErr)

	require.ErrorIs(t, repo.RestartFailed(context.Background(), running), dbErr)
	require.NoError(t, mock.ExpectationsWereMet())
}

func retryPtrInt64(v int64) *int64 { return &v }
