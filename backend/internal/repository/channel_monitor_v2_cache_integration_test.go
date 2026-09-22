//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2CacheEligibilityIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("channel_cache_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	}()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	for _, migration := range []string{"194_channel_monitor_v2.sql", "199_channel_monitor_v2_fixed_rollups.sql", "263_channel_monitor_v2_cache_eligibility.sql"} {
		raw, err := os.ReadFile("../../migrations/" + migration)
		require.NoError(t, err)
		exec(string(raw))
	}
	exec(`CREATE TABLE groups (id BIGINT,platform TEXT);
CREATE TABLE accounts (id BIGINT,platform TEXT);
CREATE TABLE usage_logs (id BIGSERIAL,created_at TIMESTAMPTZ,group_id BIGINT DEFAULT 8,account_id BIGINT DEFAULT 62,user_id BIGINT DEFAULT 7,
 model TEXT DEFAULT 'model-a', requested_model TEXT,request_id TEXT,request_type INT, stream BOOLEAN,openai_ws_mode BOOLEAN DEFAULT false,
 actual_cost NUMERIC DEFAULT 1,input_tokens BIGINT,output_tokens BIGINT DEFAULT 10,cache_creation_tokens BIGINT DEFAULT 0,cache_read_tokens BIGINT,
 first_token_ms INT,duration_ms INT);
INSERT INTO groups VALUES (8,'openai'); INSERT INTO accounts VALUES (62,'openai');`)
	start := time.Now().UTC().Truncate(time.Hour)
	end := start.Add(time.Hour)
	exec(`INSERT INTO usage_logs(created_at,request_id,request_type,stream,openai_ws_mode,input_tokens,cache_creation_tokens,cache_read_tokens) VALUES
 ($1,'sync',1,false,false,1000,30,50),($1,'stream',2,true,false,10,0,90),($1,'ws',3,false,true,10,0,90),($1,'legacy-sync',0,false,false,2000,0,0)`, start)
	for _, query := range []string{channelMonitorV2UsageMetricsSQL, channelMonitorV2UserMetricsSQL} {
		exec(fmt.Sprintf(query, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL), start, end)
	}
	for _, query := range []string{channelMonitorV2MetricsRollupSQL, channelMonitorV2UserMetricsRollupSQL} {
		exec(query, "1 hour", 3600, start, end)
	}
	for _, table := range []string{"channel_monitor_v2_metrics_1m", "channel_monitor_v2_user_metrics_1m", "channel_monitor_v2_metrics_rollup", "channel_monitor_v2_user_metrics_rollup"} {
		var f channelMonitorV2Fact
		require.NoError(t, db.QueryRowContext(ctx, `SELECT success_requests,input_tokens,output_tokens,cache_creation_tokens,cache_read_tokens,cache_eligible_input_tokens,cache_eligible_creation_tokens,cache_eligible_read_tokens FROM `+table).Scan(&f.Success, &f.Input, &f.Output, &f.CacheCreation, &f.CacheRead, &f.CacheEligibleInput, &f.CacheEligibleCreation, &f.CacheEligibleRead))
		acc := newMetricAccumulator()
		acc.addFact(f)
		metric := acc.metric(1, true)
		require.Equal(t, int64(4), metric.RequestCount, table)
		require.Equal(t, int64(3320), metric.TokenCount, table)
		require.Equal(t, int64(230), metric.CacheReadTokens, table)
		require.Equal(t, int64(180), metric.CacheRateNumerator, table)
		require.Equal(t, int64(200), metric.CacheRateDenominator, table)
		require.InDelta(t, .9, metric.CacheRate, .00001, table)
	}
}
