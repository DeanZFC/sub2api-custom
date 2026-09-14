package repository

import (
	"context"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListAdminRevenueAggregatesWalletAndLedgerWithoutCrossMultiplication(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &sharedAccountPoolRepository{db: db}
	mock.ExpectQuery(`(?s)WITH owners AS .*SELECT COUNT\(\*\) FROM owners`).
		WithArgs("%alice%").WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT u\.id,.*total_transferred.*FROM users u.*ORDER BY COALESCE\(rev\.owner_amount,0\) DESC`).
		WithArgs("%alice%", 50, 0).
		WillReturnRows(sqlmock.NewRows([]string{"user_id", "username", "email", "listing_count", "request_count", "gross_amount", "platform_fee", "owner_amount", "pending", "available", "frozen", "total_earned", "total_transferred", "last_request_at"}).
			AddRow(42, "alice", "alice@example.com", 2, 3, "100.00000000", "10.00000000", "90.00000000", "2", "88", "0", "90", "0", now))
	items, total, err := repo.ListAdminRevenue(context.Background(), service.SharedPoolRevenueQuery{Search: "alice", Page: 1, PageSize: 50})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, "90.00000000", items[0].OwnerAmount)
	require.EqualValues(t, 3, items[0].RequestCount)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestListAdminRevenueRecordsFiltersSharedLedgerAndPaginates(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	repo := &sharedAccountPoolRepository{db: db}
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	end := start.AddDate(0, 0, 1)
	// The query must retain action='earn' and apply all supplied filters.
	mock.ExpectQuery(`(?s)SELECT COUNT\(\*\) FROM shared_account_usage_ledger e.*e\.action='earn'.*e\.owner_user_id=\$1.*e\.listing_id=\$2.*cs\.model=\$3.*e\.created_at >= \$4.*e\.created_at < \$5`).
		WithArgs(int64(42), int64(7), "gpt-5.4", start, end).WillReturnRows(sqlmock.NewRows([]string{"count"}).AddRow(1))
	now := time.Now().UTC()
	mock.ExpectQuery(`(?s)SELECT e\.id,e\.listing_id.*FROM shared_account_usage_ledger e.*e\.action='earn'.*ORDER BY e\.created_at DESC`).
		WithArgs(int64(42), int64(7), "gpt-5.4", start, end, 20, 20).
		WillReturnRows(sqlmock.NewRows([]string{"id", "listing_id", "display_name", "platform", "owner_user_id", "owner_name", "owner_email", "consumer_user_id", "consumer_name", "consumer_email", "request_id", "model", "result_status", "duration_ms", "gross_cost", "platform_fee", "owner_amount", "frozen_until", "released_at", "created_at"}).
			AddRow(9, 7, "shared", "openai", 42, "alice", "alice@example.com", 99, "bob", "bob@example.com", "hash", "gpt-5.4", "success", 120, "1", "0.1", "0.9", nil, now, now))
	items, total, err := repo.ListAdminRevenueRecords(context.Background(), service.SharedPoolRevenueQuery{OwnerID: ptrInt64(42), ListingID: ptrInt64(7), Model: "gpt-5.4", StartTime: &start, EndTime: &end, Page: 2, PageSize: 20})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Len(t, items, 1)
	require.Equal(t, int64(99), items[0].ConsumerUserID)
	require.Equal(t, "hash", items[0].RequestID)
	require.NoError(t, mock.ExpectationsWereMet())
}

func ptrInt64(v int64) *int64 { return &v }
