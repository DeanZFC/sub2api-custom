package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestSharedCardsFetchPageAndRecentCallsInOneQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &sharedAccountPoolRepository{db: db}
	now := time.Now().UTC()
	rows := sqlmock.NewRows([]string{"id", "account_id", "platform", "account_type", "display_name", "uploader_name", "status", "listed", "concurrency_limit", "concurrency_multiplier", "sell_rate", "total_call_count", "last_called_at", "calls"}).
		AddRow(7, 11, "openai", "apikey", "My account", "alice", "active", true, 3, 1, 2, 123, now, `[{"request_id":"r1","model":"","result_status":"success","duration_ms":10,"charged_amount":0.1,"created_at":"2026-09-09T00:00:00Z"}]`)
	mock.ExpectQuery(`(?s)WHERE l\.deleted_at IS NULL.*a\.account_scope = 'shared'.*platform = \$3.*LEFT JOIN LATERAL`).WithArgs(50, 5, "openai").WillReturnRows(rows)
	cards, err := repo.ListPublicCards(context.Background(), "openai", 999, 999)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.EqualValues(t, 123, cards[0].TotalCallCount)
	require.Equal(t, "apikey", cards[0].Type)
	require.Len(t, cards[0].RecentCalls, 1)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSharedOwnerCardsIncludePausedAccountsAndEnforceOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &sharedAccountPoolRepository{db: db}
	rows := sqlmock.NewRows([]string{"id", "account_id", "platform", "account_type", "display_name", "uploader_name", "status", "listed", "concurrency_limit", "concurrency_multiplier", "sell_rate", "total_call_count", "last_called_at", "calls"}).AddRow(7, 11, "openai", "oauth", "My account", "alice", "paused", true, 3, 1, 2, 0, nil, `[]`)
	mock.ExpectQuery(`(?s)WHERE l\.deleted_at IS NULL.*a\.account_scope = 'shared'.*owner_user_id = \$3.*status <> 'deleted'.*LEFT JOIN LATERAL`).WithArgs(200, 10, int64(42)).WillReturnRows(rows)
	cards, err := repo.GetOwnerCards(context.Background(), 42, 200, 10)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.Equal(t, "paused", cards[0].Status)
	require.Equal(t, "oauth", cards[0].Type)
	require.NotNil(t, cards[0].RecentCalls)
	_, err = repo.GetOwnerCards(context.Background(), 0, 200, 10)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}
