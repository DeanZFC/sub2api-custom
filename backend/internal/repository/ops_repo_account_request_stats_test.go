package repository

import (
	"context"
	"regexp"
	"testing"
	"time"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestOpsAccountRequestStatsReadsOneAggregateWithoutPagination(t *testing.T) {
	db, mock := newSQLMock(t)
	repo := &opsRepository{db: db}
	end := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	start := end.Add(-time.Hour)
	mock.ExpectQuery(regexp.QuoteMeta(opsAccountRequestStatsSQL)).
		WithArgs(start, end, "{1,2}").
		WillReturnRows(sqlmock.NewRows([]string{"account_id", "cache_rate", "avg_first_token_ms", "avg_duration_ms", "cache_samples", "first_token_samples", "duration_samples"}).
			AddRow(1, 0.8, 0, 0, 25, 25, 30).
			AddRow(2, nil, nil, nil, 0, 0, 0))
	stats, err := repo.GetAccountRequestStats(context.Background(), []int64{1, 2}, start, end)
	require.NoError(t, err)
	require.Len(t, stats, 2)
	require.InDelta(t, 0.8, *stats["1"].CacheRate, 1e-9)
	require.EqualValues(t, 25, stats["1"].CacheSamples)
	require.NotNil(t, stats["1"].AvgFirstTokenMs)
	require.Zero(t, *stats["1"].AvgFirstTokenMs)
	require.Nil(t, stats["2"].CacheRate)
	require.Nil(t, stats["2"].AvgFirstTokenMs)
	require.Nil(t, stats["2"].AvgDurationMs)
	require.NotContains(t, opsAccountRequestStatsSQL, "LIMIT")
	require.NoError(t, mock.ExpectationsWereMet())
}
