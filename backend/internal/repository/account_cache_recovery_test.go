package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestAccountCacheRecoveryDatabaseErrorsFailClosed(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	mock.ExpectQuery(`(?s)SELECT status='active'.*FROM accounts WHERE id=\$1`).WithArgs(int64(41)).WillReturnError(errors.New("database unavailable"))
	allowed, err := (&accountRepository{sql: db}).AcquireCacheRecoveryRequest(context.Background(), 41)
	require.ErrorContains(t, err, "database unavailable")
	require.False(t, allowed)
	require.NoError(t, mock.ExpectationsWereMet())
	allowed, err = (&accountRepository{}).AcquireCacheRecoveryRequest(context.Background(), 41)
	require.Error(t, err)
	require.False(t, allowed)
}
