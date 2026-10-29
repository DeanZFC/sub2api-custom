//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestOpsAccountRequestStatsFullHourIntegration(t *testing.T) {
	ctx := context.Background()
	// A private connection and temporary table exercise nullable historical
	// telemetry without changing the migrated shared integration database.
	db, err := sql.Open("postgres", integrationDatabaseDSN)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.ExecContext(ctx, `CREATE TEMP TABLE usage_logs (
 account_id BIGINT, created_at TIMESTAMPTZ, request_type SMALLINT, stream BOOLEAN,
 openai_ws_mode BOOLEAN, input_tokens BIGINT, cache_read_tokens BIGINT,
 cache_creation_tokens BIGINT, image_input_tokens BIGINT, first_token_ms BIGINT, duration_ms BIGINT)`)
	require.NoError(t, err)
	end := time.Date(2026, 10, 29, 9, 0, 0, 0, time.UTC)
	start := end.Add(-time.Hour)
	insert := func(account int64, at time.Time, requestType int, stream, ws bool, input, read, creation, image, first, duration any) {
		_, err := db.ExecContext(ctx, `INSERT INTO usage_logs VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`, account, at, requestType, stream, ws, input, read, creation, image, first, duration)
		require.NoError(t, err)
	}
	for i := 0; i < 25; i++ {
		insert(1, end.Add(-time.Duration(i+1)*time.Minute), 2, true, false, 100, 900, 0, 0, 1000, 2000)
	}
	insert(1, start, 2, true, false, 100, 900, 0, 0, 1000, 2000)
	insert(1, start.Add(-time.Microsecond), 2, true, false, 99999, 0, 0, 0, 99999, 99999)
	insert(1, end, 2, true, false, 99999, 0, 0, 0, 99999, 99999)
	insert(99, end.Add(-time.Minute), 2, true, false, 99999, 0, 0, 0, 99999, 99999)
	insert(2, start, 2, true, false, 100, 0, 0, nil, 0, 0)
	insert(2, start, 2, true, false, nil, nil, nil, nil, nil, nil)
	insert(3, start, 1, true, true, 0, 99999, 0, 0, 99999, 1000)    // Explicit sync overrides flags.
	insert(3, start, 4, true, false, 0, 99999, 0, 0, 99999, 1000)   // Cyber is not cache/TTFT telemetry.
	insert(3, start, 0, true, false, 200, 800, 100, 100, 500, 1000) // Legacy stream; remove image tokens.
	insert(3, start, 3, false, true, 100, 0, 0, 0, 1500, 3000)      // WS with stream=false remains eligible.
	insert(3, start, 3, false, false, 100, 0, 0, 0, 1000, 1500)     // Explicit WS type survives missing legacy flags.
	insert(4, start, 2, true, false, 0, 0, 0, 0, nil, nil)          // Zero denominator is unavailable.
	insert(4, start, 2, true, false, nil, 800, 0, 0, -1, -1)        // Missing/negative telemetry is excluded.
	insert(4, start, 2, true, false, 100, 800, -1, 0, nil, nil)
	insert(4, start, 2, true, false, 100, 800, 0, -1, nil, nil)

	stats, err := (&opsRepository{db: db}).GetAccountRequestStats(ctx, []int64{1, 2, 3, 4, 5}, start, end)
	require.NoError(t, err)
	require.Len(t, stats, 4)
	require.EqualValues(t, 26, stats["1"].CacheSamples, "all requests in the hour contribute beyond the ten timeline entries")
	require.EqualValues(t, 26, stats["1"].FirstTokenSamples)
	require.EqualValues(t, 26, stats["1"].DurationSamples)
	require.InDelta(t, 0.9, *stats["1"].CacheRate, 1e-9)
	require.InDelta(t, 1000, *stats["1"].AvgFirstTokenMs, 1e-9)
	require.InDelta(t, 2000, *stats["1"].AvgDurationMs, 1e-9)
	require.EqualValues(t, 1, stats["2"].CacheSamples)
	require.Zero(t, *stats["2"].CacheRate)
	require.Zero(t, *stats["2"].AvgFirstTokenMs)
	require.Zero(t, *stats["2"].AvgDurationMs)
	require.EqualValues(t, 3, stats["3"].CacheSamples)
	require.EqualValues(t, 3, stats["3"].FirstTokenSamples)
	require.EqualValues(t, 5, stats["3"].DurationSamples)
	require.InDelta(t, 800.0/1200, *stats["3"].CacheRate, 1e-9)
	require.InDelta(t, 1000, *stats["3"].AvgFirstTokenMs, 1e-9)
	require.InDelta(t, 1500, *stats["3"].AvgDurationMs, 1e-9)
	require.Zero(t, stats["4"].CacheSamples)
	require.Nil(t, stats["4"].CacheRate)
	require.Nil(t, stats["4"].AvgFirstTokenMs)
	require.Nil(t, stats["4"].AvgDurationMs)
	require.Nil(t, stats["5"])
}
