package repository

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestListPlatformCounts(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "all platforms", true: "empty accounts"}[empty], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			repo := newAccountRepositoryWithSQL(nil, db, nil)
			rows := sqlmock.NewRows([]string{"platform", "count"})
			want := []service.AccountPlatformCount{}
			if !empty {
				rows.AddRow("anthropic", 27).AddRow("openai", 1501).AddRow("unregistered", 3)
				want = []service.AccountPlatformCount{{Platform: "anthropic", Count: 27}, {Platform: "openai", Count: 1501}, {Platform: "unregistered", Count: 3}}
			}
			// Only soft deletion is excluded. No pagination, status or scheduler
			// predicate may hide a platform from the account-management tabs.
			mock.ExpectQuery(regexp.QuoteMeta("SELECT platform, COUNT(*) FROM accounts WHERE deleted_at IS NULL GROUP BY platform ORDER BY platform")).
				WillReturnRows(rows).RowsWillBeClosed()
			got, err := repo.ListPlatformCounts(context.Background())
			require.NoError(t, err)
			require.Equal(t, want, got)
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}

func TestListPlatformCountsDoesNotReturnPartialResultsOnRowError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	rows := sqlmock.NewRows([]string{"platform", "count"}).AddRow("openai", 10).AddRow("anthropic", 20).
		RowError(1, errors.New("query interrupted"))
	mock.ExpectQuery("SELECT platform, COUNT").WillReturnRows(rows).RowsWillBeClosed()
	repo := newAccountRepositoryWithSQL(nil, db, nil)
	_, err = repo.ListPlatformCounts(context.Background())
	require.EqualError(t, err, "query interrupted")
	require.NoError(t, mock.ExpectationsWereMet())
}
