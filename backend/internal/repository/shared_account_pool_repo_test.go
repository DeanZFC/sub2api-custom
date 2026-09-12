package repository

import (
	"context"
	"encoding/json"
	"regexp"
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
	rows := sqlmock.NewRows([]string{"id", "account_id", "platform", "account_type", "display_name", "uploader_name", "status", "listed", "concurrency_limit", "concurrency_multiplier", "sell_rate", "total_call_count", "last_called_at", "available_models", "calls"}).
		AddRow(7, 11, "openai", "apikey", "My account", "alice", "active", true, 3, 1, 2, 123, now, `["gpt-5.4"]`, `[{"request_id":"r1","model":"","result_status":"success","duration_ms":10,"charged_amount":0.1,"created_at":"2026-09-09T00:00:00Z"}]`)
	mock.ExpectQuery(`(?s)WHERE l\.deleted_at IS NULL.*a\.account_scope = 'shared'.*platform = \$3.*LEFT JOIN LATERAL`).WithArgs(50, 5, "openai").WillReturnRows(rows)
	cards, err := repo.ListPublicCards(context.Background(), "openai", 999, 999)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.EqualValues(t, 123, cards[0].TotalCallCount)
	require.Equal(t, "apikey", cards[0].Type)
	require.Equal(t, []string{"gpt-5.4"}, cards[0].AvailableModels)
	require.Len(t, cards[0].RecentCalls, 1)
	// Public cards must not expose internal correlation IDs or settlement
	// amounts; those remain available to owner/admin views.
	require.Empty(t, cards[0].RecentCalls[0].RequestID)
	require.Zero(t, cards[0].RecentCalls[0].ChargedAmount)
	require.Empty(t, cards[0].UploaderName)
	encoded, marshalErr := json.Marshal(cards[0].RecentCalls[0])
	require.NoError(t, marshalErr)
	require.NotContains(t, string(encoded), "request_id")
	require.NotContains(t, string(encoded), "charged_amount")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSharedOwnerCardsIncludePausedAccountsAndEnforceOwner(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &sharedAccountPoolRepository{db: db}
	rows := sqlmock.NewRows([]string{"id", "account_id", "platform", "account_type", "display_name", "uploader_name", "status", "listed", "concurrency_limit", "concurrency_multiplier", "sell_rate", "total_call_count", "last_called_at", "available_models", "calls"}).AddRow(7, 11, "openai", "oauth", "My account", "alice", "paused", true, 3, 1, 2, 0, nil, `[]`, `[{"request_id":"owner-r1","result_status":"success","charged_amount":1.25,"created_at":"2026-09-09T00:00:00Z"}]`)
	mock.ExpectQuery(`(?s)WHERE l\.deleted_at IS NULL.*a\.account_scope = 'shared'.*owner_user_id = \$3.*status <> 'deleted'.*LEFT JOIN LATERAL`).WithArgs(200, 10, int64(42)).WillReturnRows(rows)
	cards, err := repo.GetOwnerCards(context.Background(), 42, 200, 10)
	require.NoError(t, err)
	require.Len(t, cards, 1)
	require.Equal(t, "paused", cards[0].Status)
	require.Equal(t, "oauth", cards[0].Type)
	require.NotNil(t, cards[0].RecentCalls)
	require.Equal(t, "owner-r1", cards[0].RecentCalls[0].RequestID)
	require.Equal(t, 1.25, cards[0].RecentCalls[0].ChargedAmount)
	_, err = repo.GetOwnerCards(context.Background(), 0, 200, 10)
	require.Error(t, err)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestSetListingListedSynchronizesSchedulableAndOutbox(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	r := &sharedAccountPoolRepository{db: db}
	mock.ExpectBegin()
	mock.ExpectQuery(regexp.QuoteMeta("UPDATE shared_account_listings SET listed=$3,updated_at=NOW()")).WithArgs(int64(8), int64(42), false).WillReturnRows(sqlmock.NewRows([]string{"account_id", "status"}).AddRow(int64(7), "active"))
	mock.ExpectExec(regexp.QuoteMeta("UPDATE accounts SET schedulable=$2,updated_at=NOW()")).WithArgs(int64(7), false).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectExec(regexp.QuoteMeta("INSERT INTO scheduler_outbox")).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectCommit()
	require.NoError(t, r.SetListingListed(context.Background(), 42, 8, false))
	require.NoError(t, mock.ExpectationsWereMet())
}
