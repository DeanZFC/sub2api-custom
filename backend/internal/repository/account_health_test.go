package repository

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountHealthIsolationUsesConditionalAtomicOutbox(t *testing.T) {
	for _, tc := range []struct {
		name      string
		automatic bool
		rows      int64
		err       error
	}{
		{"automatic applied", true, 1, nil},
		{"new cooldown or disabled policy wins", true, 0, nil},
		{"manual cannot replace foreign cooldown", false, 0, nil},
		{"outbox failure returns failure", true, 0, errors.New("outbox unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := newAccountRepositoryWithSQL(nil, db, nil)
			until := time.Now().Add(time.Hour)
			reason := "health:auto err_rate=90%"
			if !tc.automatic {
				reason = "health:manual"
			}
			expect := mock.ExpectExec(`(?s)WITH changed AS.*temp_unschedulable_until <= NOW\(\).*NOT \$4::boolean.*temp_unschedulable_reason LIKE 'health:%'.*account_protection_policy,enabled.*account_protection_policy,mode.*INSERT INTO scheduler_outbox.*FROM changed`).
				WithArgs(until, reason, int64(1), tc.automatic, service.SchedulerOutboxEventAccountChanged)
			if tc.err != nil {
				expect.WillReturnError(tc.err)
			} else {
				expect.WillReturnResult(sqlmock.NewResult(0, tc.rows))
			}
			changed, err := repo.TrySetAccountHealthIsolation(context.Background(), 1, until, reason, tc.automatic)
			require.Equal(t, tc.rows > 0, changed)
			require.ErrorIs(t, err, tc.err)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestAccountHealthRecoveryUsesScopedAtomicOutbox(t *testing.T) {
	for _, automatic := range []bool{true, false} {
		db, mock, err := sqlmock.New()
		require.NoError(t, err)
		t.Cleanup(func() { _ = db.Close() })
		repo := newAccountRepositoryWithSQL(nil, db, nil)
		prefix := "health:"
		if automatic {
			prefix = "health:auto"
		}
		mock.ExpectExec(`(?s)WITH changed AS.*temp_unschedulable_reason LIKE \$2.*INSERT INTO scheduler_outbox.*FROM changed`).
			WithArgs(int64(1), prefix, service.SchedulerOutboxEventAccountChanged).
			WillReturnResult(sqlmock.NewResult(0, 0))
		changed, err := repo.ClearAccountHealthIsolation(context.Background(), 1, automatic)
		require.NoError(t, err)
		require.False(t, changed)
		require.NoError(t, mock.ExpectationsWereMet())
	}
}
