package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestFinalizeCodexQuotaOverdraftProbeFailedIsAtomic(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	until := now.Add(5 * time.Hour)
	state := &service.CodexQuotaOverdraftProbeState{
		Status:      "failed",
		CycleKey:    "5h:quota-cycle",
		QuotaWindow: "5h",
		Attempts:    5,
		Limit:       5,
		StartedAt:   now,
		RecoverAt:   &until,
	}
	reason := service.BuildTempUnschedReasonPayload("codex_quota_overdraft", "quota exhausted")

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts.*temp_unschedulable_until.*codex_quota_overdraft_probe`).
		WithArgs(service.CodexQuotaOverdraftProbeExtraKey, sqlmock.AnyArg(), until, reason, int64(77), state.CycleKey).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key)")).
		WithArgs(service.SchedulerOutboxEventAccountChanged, int64(77), nil, nil, sqlmock.AnyArg()).
		WillReturnResult(sqlmock.NewResult(1, 1))
	mock.ExpectCommit()

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	finalized, err := repo.FinalizeCodexQuotaOverdraftProbeFailed(context.Background(), 77, state, until, reason)

	require.NoError(t, err)
	require.True(t, finalized)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestFinalizeCodexQuotaOverdraftProbeFailedRollsBackWhenOutboxFails(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	until := now.Add(5 * time.Hour)
	state := &service.CodexQuotaOverdraftProbeState{
		Status:      "failed",
		CycleKey:    "5h:quota-cycle",
		QuotaWindow: "5h",
		Attempts:    5,
		Limit:       5,
		StartedAt:   now,
		RecoverAt:   &until,
	}

	mock.ExpectBegin()
	mock.ExpectExec(`(?s)UPDATE accounts.*temp_unschedulable_until.*codex_quota_overdraft_probe`).
		WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload, dedup_key)")).
		WillReturnError(errors.New("outbox unavailable"))
	mock.ExpectRollback()

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	finalized, err := repo.FinalizeCodexQuotaOverdraftProbeFailed(context.Background(), 77, state, until, "quota exhausted")

	require.ErrorContains(t, err, "outbox unavailable")
	require.False(t, finalized)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListDueCodexQuotaOverdraftProbesIncludesInconclusiveAndStalePending(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })

	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT id.*status}' = 'inconclusive'.*retry_count.*status}' = 'pending'.*INTERVAL '2 minutes'`).
		WithArgs(service.PlatformOpenAI, service.AccountTypeOAuth, service.StatusActive, now, 3, 100).
		WillReturnRows(sqlmock.NewRows([]string{"id"}))

	repo := newAccountRepositoryWithSQL(nil, db, nil)
	accounts, err := repo.ListDueCodexQuotaOverdraftProbes(context.Background(), now, 100)

	require.NoError(t, err)
	require.Empty(t, accounts)
	require.NoError(t, mock.ExpectationsWereMet())
}
