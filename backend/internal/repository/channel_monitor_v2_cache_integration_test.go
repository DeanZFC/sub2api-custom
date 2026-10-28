//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2OfficialCacheRateIntegration(t *testing.T) {
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
	t.Run("TPS migration preserves retained history without original logs", func(t *testing.T) {
		historical := time.Now().UTC().Add(-60 * 24 * time.Hour).Truncate(time.Hour)
		tables := []string{"channel_monitor_v2_metrics_1m", "channel_monitor_v2_user_metrics_1m", "channel_monitor_v2_metrics_rollup", "channel_monitor_v2_user_metrics_rollup"}
		before := make(map[string]string)
		for _, table := range tables {
			columns, values := "", ""
			if strings.Contains(table, "user_metrics") {
				columns, values = columns+",user_id", values+",7"
			}
			if strings.HasSuffix(table, "_rollup") {
				columns, values = columns+",bucket_seconds", values+",86400"
			}
			exec(`INSERT INTO `+table+`(bucket_start,platform,group_id,model,success_requests,error_requests,input_tokens,cache_read_tokens`+columns+`)
 VALUES($1,'openai',8,'historical',10,3,1000,700`+values+`)`, historical)
			var encoded string
			require.NoError(t, db.QueryRowContext(ctx, `SELECT to_jsonb(m)::text FROM `+table+` m`).Scan(&encoded))
			before[table] = encoded
		}
		exec(`UPDATE channel_monitor_v2_watermarks SET usage_coverage_start=$1,error_coverage_start=$1,backfill_cursor=$1,data_through=$2,last_successful_at=$2`, historical, historical.Add(time.Hour))
		var watermark string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_jsonb(w)::text FROM channel_monitor_v2_watermarks w WHERE id=1`).Scan(&watermark))
		raw, err := os.ReadFile("../../migrations/316_channel_monitor_output_tps.sql")
		require.NoError(t, err)
		exec(string(raw))
		for _, table := range tables {
			var encoded string
			var tokens, duration, samples int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT (to_jsonb(m)-'tps_output_tokens'-'tps_generation_ms'-'tps_samples')::text,tps_output_tokens,tps_generation_ms,tps_samples FROM `+table+` m`).Scan(&encoded, &tokens, &duration, &samples))
			require.JSONEq(t, before[table], encoded, "existing counters must survive even when raw logs have expired")
			require.Zero(t, tokens)
			require.Zero(t, duration)
			require.Zero(t, samples)
			require.Nil(t, service.OutputTokensPerSecond(tokens, float64(duration)))
			exec(`TRUNCATE ` + table)
		}
		var after string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT to_jsonb(w)::text FROM channel_monitor_v2_watermarks w WHERE id=1`).Scan(&after))
		require.JSONEq(t, watermark, after, "TPS must not restart the destructive full-metric historical backfill")
	})
	rawTPS, err := os.ReadFile("../../migrations/316_channel_monitor_output_tps.sql")
	require.NoError(t, err)
	exec(string(rawTPS))
	exec(`CREATE TABLE groups (id BIGINT,platform TEXT);
CREATE TABLE accounts (id BIGINT,platform TEXT);
CREATE TABLE usage_logs (id BIGSERIAL,created_at TIMESTAMPTZ,group_id BIGINT DEFAULT 8,account_id BIGINT DEFAULT 62,user_id BIGINT DEFAULT 7,api_key_id BIGINT DEFAULT 1,
 model TEXT DEFAULT 'model-a', requested_model TEXT,request_id TEXT,request_type INT, stream BOOLEAN,openai_ws_mode BOOLEAN DEFAULT false,
 actual_cost NUMERIC DEFAULT 1,input_tokens BIGINT,output_tokens BIGINT DEFAULT 10,cache_creation_tokens BIGINT DEFAULT 0,cache_read_tokens BIGINT,
 first_token_ms INT,duration_ms INT,image_count INT DEFAULT 0,video_count INT DEFAULT 0,image_output_tokens INT DEFAULT 0,
 inbound_endpoint TEXT,upstream_endpoint TEXT,native_compaction_v2 BOOLEAN DEFAULT false);
CREATE TABLE ops_error_logs(id BIGSERIAL,created_at TIMESTAMPTZ,account_id BIGINT DEFAULT 62,api_key_id BIGINT DEFAULT 1,
 request_id TEXT,client_request_id TEXT,request_type INT DEFAULT 2,is_count_tokens BOOLEAN DEFAULT false,status_code INT DEFAULT 503,error_type TEXT DEFAULT 'upstream_error');
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
		require.Equal(t, int64(230), metric.CacheRateNumerator, table)
		require.Equal(t, int64(3280), metric.CacheRateDenominator, table)
		require.True(t, metric.HasCacheSamples, table)
		require.InDelta(t, 230.0/3280.0, metric.CacheRate, .00001, table)
		// Eligibility columns remain maintained for compatibility; they no longer
		// supply the cache health signal for minute facts or rollups.
		require.Equal(t, int64(20), f.CacheEligibleInput, table)
		require.Equal(t, int64(180), f.CacheEligibleRead, table)
		thresholds := service.DefaultChannelMonitorV2HealthThresholds()
		thresholds.MinimumSample = 1
		thresholds.WarningCacheRate, thresholds.CriticalCacheRate = 0.85, 0.60
		health := service.ChannelMonitorV2HealthForWithThresholds(metric, thresholds)
		require.NotNil(t, health.CacheScore, table)
		require.NotNil(t, health.Score, table)
		cacheScore := 100 * 230.0 / 3280.0
		require.InDelta(t, cacheScore, *health.CacheScore, .00001, table)
		require.InDelta(t, (100*thresholds.ErrorWeight+cacheScore*thresholds.CacheWeight)/(thresholds.ErrorWeight+thresholds.CacheWeight), *health.Score, .00001, table)
		require.Equal(t, "critical", health.Cache, table)
		require.Equal(t, "warning", health.Overall, table)
	}

	t.Run("output TPS weights valid generation time across facts and rollups", func(t *testing.T) {
		exec(`TRUNCATE usage_logs,channel_monitor_v2_metrics_1m,channel_monitor_v2_user_metrics_1m,channel_monitor_v2_metrics_rollup,channel_monitor_v2_user_metrics_rollup`)
		exec(`INSERT INTO usage_logs(created_at,request_id,request_type,stream,openai_ws_mode,input_tokens,output_tokens,first_token_ms,duration_ms) VALUES
 ($1,'slow',2,true,false,100,100,1000,11000),
 ($1+INTERVAL '1 minute','fast',2,true,false,100,100,3000,4000),
 ($1+INTERVAL '2 minutes','ws',3,false,true,100,20,100,200),
 ($1,'missing-ttft',2,true,false,100,9999,NULL,10000),
 ($1,'empty-interval',2,true,false,100,9999,10000,10000),
 ($1,'negative-ttft',2,true,false,100,9999,-1,10000),
 ($1,'sync',1,false,false,100,9999,1000,10000),
 ($1,'cyber',4,true,false,100,9999,1000,10000),
 ($1,'count',6,true,false,100,9999,1000,10000),
 ($1,'media',2,true,false,100,9999,1000,10000),
 ($1,'audio',2,true,false,100,9999,1000,10000),
 ($1,'compact',2,true,false,100,9999,1000,10000),
 ($1,'failed',2,true,false,100,9999,1000,10000),
 ($1,'free-success',2,true,false,100,40,1000,2000),
 ($1,'local:paid-partial',2,true,false,100,9999,1000,10000)`, start)
		exec(`
 UPDATE usage_logs SET image_output_tokens=1 WHERE request_id='media';
 UPDATE usage_logs SET inbound_endpoint='/v1/audio/speech' WHERE request_id='audio';
 UPDATE usage_logs SET native_compaction_v2=true WHERE request_id='compact';
 UPDATE usage_logs SET actual_cost=0 WHERE request_id IN('failed','free-success')`)
		exec(`INSERT INTO ops_error_logs(created_at,request_id,request_type,status_code) VALUES
 ($1,'failed',2,503),($1,'paid-partial',2,503),($1,'fast',2,0),($1,'ws',3,503)`, start)
		for _, query := range []string{channelMonitorV2UsageMetricsSQL, channelMonitorV2UserMetricsSQL} {
			exec(fmt.Sprintf(query, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL), start, end)
		}
		for _, query := range []string{channelMonitorV2MetricsRollupSQL, channelMonitorV2UserMetricsRollupSQL} {
			exec(query, "1 hour", 3600, start, end)
		}
		for _, table := range []string{"channel_monitor_v2_metrics_1m", "channel_monitor_v2_user_metrics_1m", "channel_monitor_v2_metrics_rollup", "channel_monitor_v2_user_metrics_rollup"} {
			var f channelMonitorV2Fact
			require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(tps_output_tokens),SUM(tps_generation_ms),SUM(tps_samples) FROM `+table).Scan(&f.TPSOutputTokens, &f.TPSGenerationMS, &f.TPSSamples))
			require.EqualValues(t, 260, f.TPSOutputTokens, table)
			require.EqualValues(t, 12100, f.TPSGenerationMS, table)
			require.EqualValues(t, 4, f.TPSSamples, table)
			acc := newMetricAccumulator()
			acc.addFact(f)
			metric := acc.metric(60, true)
			require.NotNil(t, metric.OutputTPS, table)
			require.InDelta(t, 260.0/12.1, *metric.OutputTPS, .00001, table)
			require.EqualValues(t, 4, metric.TPSSamples, table)
		}
		// Exercise the actual monitoring reader, including its column scan and
		// time-bucket merge, for both minute facts and coarser historical data.
		exec(`ALTER TABLE groups ADD COLUMN name TEXT DEFAULT 'Test group'`)
		repo := &channelMonitorV2Repository{db: db}
		cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}
		for _, bucket := range []time.Duration{time.Minute, time.Hour} {
			facts, err := repo.loadFacts(ctx, service.ChannelMonitorV2Filter{Start: start, End: end, Bucket: bucket}, cfg, true)
			require.NoError(t, err)
			acc := newMetricAccumulator()
			for _, f := range facts {
				acc.addFact(f)
			}
			metric := acc.metric(60, true)
			require.NotNil(t, metric.OutputTPS)
			require.InDelta(t, 260.0/12.1, *metric.OutputTPS, .00001)
			require.EqualValues(t, 4, metric.TPSSamples)
		}
	})
	t.Run("terminal error correlation preserves API key account and websocket boundaries", func(t *testing.T) {
		exec(`TRUNCATE usage_logs,ops_error_logs,channel_monitor_v2_metrics_1m,channel_monitor_v2_user_metrics_1m`)
		exec(`INSERT INTO usage_logs(created_at,request_id,request_type,stream,openai_ws_mode,input_tokens,output_tokens,first_token_ms,duration_ms) VALUES
 ($1,'other-key',2,true,false,100,100,500,1500),
 ($1,'other-account',2,true,false,100,100,500,1500),
 ($1,'connection',3,false,true,100,100,500,1500),
 ($1,'local:local-failure',2,true,false,100,9999,500,1500),
 ($1,'client:client-failure',2,true,false,100,9999,500,1500),
 ($1,'bare-failure',2,true,false,100,9999,500,1500)`, start)
		exec(`INSERT INTO ops_error_logs(created_at,request_id,client_request_id,api_key_id,account_id,request_type) VALUES
 ($1,'other-key',NULL,2,62,2),($1,'other-account',NULL,1,63,2),($1,'connection',NULL,1,62,3),
 ($1,'local-failure',NULL,1,62,2),($1,'mapped-client','client-failure',1,62,2),($1,'bare-failure',NULL,1,62,2)`, start)
		for _, query := range []string{channelMonitorV2UsageMetricsSQL, channelMonitorV2UserMetricsSQL} {
			exec(fmt.Sprintf(query, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL), start, end)
		}
		for _, table := range []string{"channel_monitor_v2_metrics_1m", "channel_monitor_v2_user_metrics_1m"} {
			var output, duration, samples int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(tps_output_tokens),SUM(tps_generation_ms),SUM(tps_samples) FROM `+table).Scan(&output, &duration, &samples))
			require.EqualValues(t, 300, output)
			require.EqualValues(t, 3000, duration)
			require.EqualValues(t, 3, samples)
		}
	})
}
