//go:build integration

package repository

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/usagestats"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestUsageCacheEligibilityIntegration(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	client := tx.Client()
	repo := newUsageLogRepositoryWithSQL(client, tx)
	user := mustCreateUser(t, client, &service.User{Email: "cache-eligibility@test.com"})
	key := mustCreateApiKey(t, client, &service.APIKey{UserID: user.ID, Key: "sk-cache-eligibility", Name: "cache"})
	account := mustCreateAccount(t, client, &service.Account{Name: "cache-eligibility"})
	start := time.Date(2024, 1, 2, 0, 0, 0, 0, time.UTC)
	end := start.Add(24 * time.Hour)
	cases := []struct {
		kind                         int
		stream, ws                   bool
		input, image, creation, read int
	}{
		{1, false, false, 10000, 0, 0, 0}, // sync
		{1, true, true, 10000, 0, 20, 50}, // explicit sync wins stale legacy flags
		{0, false, false, 10000, 0, 0, 0}, // legacy sync
		{2, true, false, 20, 10, 20, 70},
		{3, false, true, 10, 0, 0, 90},
		{0, true, false, 10, 0, 0, 90},    // legacy stream
		{4, true, false, 10000, 0, 0, 50}, // blocked rows
	}
	for _, c := range cases {
		_, err := tx.ExecContext(ctx, `INSERT INTO usage_logs
            (user_id,api_key_id,account_id,model,request_type,stream,openai_ws_mode,input_tokens,image_input_tokens,cache_creation_tokens,cache_read_tokens,output_tokens,total_cost,actual_cost,duration_ms,created_at)
            VALUES($1,$2,$3,'cache-model',$4,$5,$6,$7,$8,$9,$10,10,1,1,100,$11)`,
			user.ID, key.ID, account.ID, c.kind, c.stream, c.ws, c.input, c.image, c.creation, c.read, start.Add(time.Minute))
		require.NoError(t, err)
	}
	check := func(stats *usagestats.UsageStats, err error) {
		t.Helper()
		require.NoError(t, err)
		require.EqualValues(t, 7, stats.TotalRequests)
		require.EqualValues(t, 40040, stats.TotalInputTokens)
		require.InDelta(t, 7, stats.TotalActualCost, 1e-9)
		require.NotNil(t, stats.CacheHitRate)
		require.InDelta(t, 250.0/300.0, *stats.CacheHitRate, 1e-9)
	}
	check(repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: user.ID, StartTime: &start, EndTime: &end}))
	check(repo.GetUserStatsAggregated(ctx, user.ID, start, end))
	check(repo.GetAPIKeyStatsAggregated(ctx, key.ID, start, end))
	check(repo.GetAccountStatsAggregated(ctx, account.ID, start, end))
	check(repo.GetModelStatsAggregated(ctx, "cache-model", start, end))
	check(repo.GetGlobalStats(ctx, start, end))
	for _, filter := range []usagestats.UsageLogFilters{{UserID: user.ID}, {AccountID: account.ID}} {
		points, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, "hour", filter)
		require.NoError(t, err)
		require.Len(t, points, 1)
		require.NotNil(t, points[0].CacheHitRate)
		require.InDelta(t, 250.0/300.0, *points[0].CacheHitRate, 1e-9)
		require.EqualValues(t, 40040, points[0].InputTokens)
	}
	points, err := repo.GetUserUsageTrendByUserID(ctx, user.ID, start, end, "day")
	require.NoError(t, err)
	require.Len(t, points, 1)
	require.InDelta(t, 250.0/300.0, *points[0].CacheHitRate, 1e-9)

	syncType := int16(service.RequestTypeSync)
	stats, err := repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: user.ID, RequestType: &syncType})
	require.NoError(t, err)
	require.Nil(t, stats.CacheHitRate)
	require.Positive(t, stats.TotalInputTokens)

	aggregate := newDashboardAggregationRepositoryWithSQL(tx)
	require.NoError(t, aggregate.upsertHourlyAggregates(ctx, start, end))
	require.NoError(t, aggregate.upsertDailyAggregates(ctx, start, end))
	for _, granularity := range []string{"hour", "day"} {
		trend, err := repo.GetUsageTrendWithUsageFilters(ctx, start, end, granularity, usagestats.UsageLogFilters{})
		require.NoError(t, err)
		require.Len(t, trend, 1)
		require.InDelta(t, 250.0/300.0, *trend[0].CacheHitRate, 1e-9)
		require.EqualValues(t, 40040, trend[0].InputTokens)
	}
	// Legacy aggregate counters stay unknown, including partially rebuilt days.
	_, err = tx.ExecContext(ctx, `UPDATE usage_dashboard_hourly SET cache_rate_numerator=NULL,cache_rate_denominator=NULL WHERE bucket_start=$1`, start)
	require.NoError(t, err)
	require.NoError(t, aggregate.upsertDailyAggregates(ctx, start, end))
	for _, granularity := range []string{"hour", "day"} {
		trend, err := repo.getUsageTrendFromAggregates(ctx, start, end, granularity)
		require.NoError(t, err)
		require.Len(t, trend, 1)
		require.Nil(t, trend[0].CacheHitRate)
	}
	// A measured miss is zero; a sync-only or empty window is unavailable.
	_, err = tx.ExecContext(ctx, `UPDATE usage_logs SET cache_read_tokens=0 WHERE user_id=$1`, user.ID)
	require.NoError(t, err)
	stats, err = repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: user.ID})
	require.NoError(t, err)
	require.NotNil(t, stats.CacheHitRate)
	require.Zero(t, *stats.CacheHitRate)
	_, err = tx.ExecContext(ctx, `UPDATE usage_logs SET request_type=1 WHERE user_id=$1`, user.ID)
	require.NoError(t, err)
	stats, err = repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: user.ID})
	require.NoError(t, err)
	require.Nil(t, stats.CacheHitRate)
	require.NoError(t, aggregate.upsertHourlyAggregates(ctx, start, end))
	require.NoError(t, aggregate.upsertDailyAggregates(ctx, start, end))
	trend, err := repo.getUsageTrendFromAggregates(ctx, start, end, "day")
	require.NoError(t, err)
	require.Nil(t, trend[0].CacheHitRate)
	stats, err = repo.GetStatsWithFilters(ctx, usagestats.UsageLogFilters{UserID: user.ID, StartTime: &end})
	require.NoError(t, err)
	require.Nil(t, stats.CacheHitRate)
}

func TestUsageCacheMigrationPreservesHistoricalTotals(t *testing.T) {
	ctx := context.Background()
	tx := testEntTx(t)
	start := time.Date(2023, 1, 2, 0, 0, 0, 0, time.UTC)
	_, err := tx.ExecContext(ctx, `ALTER TABLE usage_dashboard_hourly DROP COLUMN cache_rate_numerator,DROP COLUMN cache_rate_denominator;
 ALTER TABLE usage_dashboard_daily DROP COLUMN cache_rate_numerator,DROP COLUMN cache_rate_denominator;`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_dashboard_hourly(bucket_start,total_requests,input_tokens,actual_cost) VALUES($1,100,10000,25);
 `, start)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO usage_dashboard_daily(bucket_date,total_requests,input_tokens,actual_cost) VALUES($1,100,10000,25)`, start)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `UPDATE usage_dashboard_aggregation_watermark SET last_aggregated_at=$1 WHERE id=1`, start)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/296_usage_cache_eligibility.sql")
	require.NoError(t, err)
	for i := 0; i < 2; i++ {
		_, err = tx.ExecContext(ctx, string(migration))
		require.NoError(t, err)
	}
	for _, table := range []string{"usage_dashboard_hourly", "usage_dashboard_daily"} {
		var requests, input int64
		var cost float64
		var numerator, denominator *int64
		rows, err := tx.QueryContext(ctx, "SELECT total_requests,input_tokens,actual_cost,cache_rate_numerator,cache_rate_denominator FROM "+table)
		require.NoError(t, err)
		require.True(t, rows.Next())
		require.NoError(t, rows.Scan(&requests, &input, &cost, &numerator, &denominator))
		require.NoError(t, rows.Close())
		require.EqualValues(t, 100, requests)
		require.EqualValues(t, 10000, input)
		require.Equal(t, 25.0, cost)
		require.Nil(t, numerator)
		require.Nil(t, denominator)
	}
	watermark, err := newDashboardAggregationRepositoryWithSQL(tx).GetAggregationWatermark(ctx)
	require.NoError(t, err)
	require.True(t, start.Equal(watermark))
}
