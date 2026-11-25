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

func TestChannelMonitorV2SegmentedAggregationIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = integrationDatabaseDSN
	}
	require.NotEmpty(t, dsn, "integration PostgreSQL must be initialized")
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("channel_segments_%d", time.Now().UnixNano())
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
	exec := func(t *testing.T, query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	for _, migration := range []string{"194_channel_monitor_v2.sql", "199_channel_monitor_v2_fixed_rollups.sql", "263_channel_monitor_v2_cache_eligibility.sql", "316_channel_monitor_output_tps.sql"} {
		raw, err := os.ReadFile("../../migrations/" + migration)
		require.NoError(t, err)
		exec(t, string(raw))
	}
	exec(t, `CREATE TABLE groups (id BIGINT,platform TEXT,name TEXT DEFAULT 'GPT-Pro',status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ,rate_multiplier NUMERIC DEFAULT 1);
CREATE TABLE accounts (id BIGINT,platform TEXT);
CREATE TABLE usage_logs (id BIGSERIAL,created_at TIMESTAMPTZ,group_id BIGINT DEFAULT 8,account_id BIGINT DEFAULT 62,user_id BIGINT DEFAULT 7,api_key_id BIGINT DEFAULT 1,
 model TEXT DEFAULT 'model-a',requested_model TEXT,request_id TEXT,request_type INT DEFAULT 2,stream BOOLEAN DEFAULT true,openai_ws_mode BOOLEAN DEFAULT false,
 actual_cost NUMERIC DEFAULT 1,input_tokens BIGINT DEFAULT 10,output_tokens BIGINT DEFAULT 10,cache_creation_tokens BIGINT DEFAULT 0,cache_read_tokens BIGINT DEFAULT 90,
 first_token_ms INT DEFAULT 100,duration_ms INT DEFAULT 1100,image_count INT DEFAULT 0,video_count INT DEFAULT 0,image_output_tokens INT DEFAULT 0,
 inbound_endpoint TEXT,upstream_endpoint TEXT,native_compaction_v2 BOOLEAN DEFAULT false);
CREATE TABLE ops_error_logs (id BIGSERIAL,created_at TIMESTAMPTZ,group_id BIGINT DEFAULT 8,account_id BIGINT DEFAULT 62,user_id BIGINT DEFAULT 7,api_key_id BIGINT DEFAULT 1,
 request_id TEXT,client_request_id TEXT,request_type INT DEFAULT 2,is_count_tokens BOOLEAN DEFAULT false,status_code INT DEFAULT 503,error_type TEXT DEFAULT 'upstream_error',
 platform TEXT DEFAULT 'openai',model TEXT DEFAULT 'model-a',requested_model TEXT,error_owner TEXT,error_source TEXT,upstream_status_code INT,
 error_message TEXT,upstream_error_message TEXT,upstream_error_detail TEXT,error_body TEXT,upstream_errors JSONB,error_phase TEXT,is_business_limited BOOLEAN DEFAULT false);
INSERT INTO groups VALUES (8,'openai'); INSERT INTO accounts VALUES (62,'openai');`)
	repo := &channelMonitorV2Repository{db: db}
	now := time.Now().UTC().Truncate(time.Minute)
	assertDaily := func(t *testing.T, day time.Time, successes, failures int64) {
		t.Helper()
		for _, table := range []string{"channel_monitor_v2_metrics_rollup", "channel_monitor_v2_user_metrics_rollup"} {
			var gotSuccess, gotFailure int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT success_requests,error_requests FROM `+table+` WHERE bucket_seconds=86400 AND bucket_start=$1`, day).Scan(&gotSuccess, &gotFailure))
			require.Equal(t, successes, gotSuccess, table)
			require.Equal(t, failures, gotFailure, table)
		}
	}
	reset := func(t *testing.T) {
		t.Helper()
		exec(t, `TRUNCATE usage_logs,ops_error_logs,channel_monitor_v2_metrics_1m,channel_monitor_v2_user_metrics_1m,channel_monitor_v2_latency_histograms_1m,channel_monitor_v2_error_metrics_1m,channel_monitor_v2_metrics_rollup,channel_monitor_v2_user_metrics_rollup,channel_monitor_v2_latency_histograms_rollup,channel_monitor_v2_error_metrics_rollup`)
		exec(t, `UPDATE channel_monitor_v2_watermarks SET backfill_cursor=NULL,data_through=NULL,usage_coverage_start=NULL,error_coverage_start=NULL`)
	}

	t.Run("local session block does not create a model or affect group health", func(t *testing.T) {
		reset(t)
		start := now.Add(-24 * time.Hour).Truncate(time.Hour)
		end := start.Add(time.Hour)
		exec(t, `INSERT INTO ops_error_logs(created_at,request_id,model,account_id,status_code,error_type,error_source) VALUES
 ($1,'blocked','deepseek-v4.1-flash',NULL,403,'cyber_policy_session_blocked','gateway_local'),
 ($1,'forbidden','gpt-test',62,403,'permission_error','upstream_http'),
 ($1,'policy','gpt-test',62,200,'cyber_policy','upstream_http')`, start)
		exec(t, `INSERT INTO usage_logs(created_at,request_id,model) VALUES ($1,'success','gpt-test')`, start)
		// A stale aggregate from the previous version must disappear on replay.
		exec(t, `INSERT INTO channel_monitor_v2_metrics_rollup(bucket_start,bucket_seconds,platform,group_id,model,error_requests)
 VALUES ($1,3600,'openai',8,'deepseek-v4.1-flash',1)`, start)
		require.NoError(t, repo.RecomputeRange(ctx, start, end))

		cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true}}}
		filter := service.ChannelMonitorV2Filter{Start: start, End: end, Bucket: time.Hour}
		for _, by := range []service.ChannelMonitorV2GroupBy{service.ChannelMonitorV2GroupByPlatformGroup, service.ChannelMonitorV2GroupByPlatformGroupModel} {
			matrix, err := repo.GetMatrix(ctx, filter, cfg, by, true)
			require.NoError(t, err)
			require.Len(t, matrix.Items, 1)
			row := matrix.Items[0]
			if by == service.ChannelMonitorV2GroupByPlatformGroupModel {
				require.Equal(t, "gpt-test", row.Model)
			}
			require.EqualValues(t, 1, row.Metrics.SuccessRequests)
			require.EqualValues(t, 2, row.Metrics.ErrorRequests)
		}
		details, err := repo.loadErrorDetails(ctx, filter, cfg)
		require.NoError(t, err)
		require.Len(t, details["upstream_forbidden"], 1)
		require.Equal(t, "gpt-test", details["upstream_forbidden"][0].Model)
		require.Len(t, details["content_policy"], 1)
	})

	t.Run("upgrade removes proven block-only history and keeps mixed buckets and audit logs", func(t *testing.T) {
		reset(t)
		day := now.Truncate(24 * time.Hour).Add(-24 * time.Hour)
		end := day.Add(24 * time.Hour)
		exec(t, `INSERT INTO ops_error_logs(created_at,request_id,model,status_code,error_type,error_source,account_id) VALUES
 ($1,'blocked','foreign-model',403,'cyber_policy_session_blocked','gateway_local',NULL),
 ($1,'blocked','foreign-model',403,'cyber_policy_session_blocked','gateway_local',NULL),
 ($1,'mixed-block','mixed-model',403,'cyber_policy_session_blocked','gateway_local',NULL),
 ($1,'denied','provider-model',403,'provider_error','upstream_http',62),
 ($1,'outage','mixed-model',503,'provider_error','upstream_http',62),
 ($1,'ambiguous-block','two-403s',403,'cyber_policy_session_blocked','gateway_local',NULL),
 ($1,'ambiguous-upstream','two-403s',403,'provider_error','upstream_http',62)`, day)
		exec(t, `INSERT INTO usage_logs(created_at,request_id,model) VALUES ($1,'ok','mixed-model')`, day)
		// Reproduce the persisted counters produced by the old error predicate.
		legacy := strings.Replace(channelMonitorV2ErrorAggregationSQL, channelMonitorV2FailureSQL("current_error"),
			"(current_error.status_code >= 400 OR current_error.error_type = 'cyber_policy')", 1)
		exec(t, fmt.Sprintf(channelMonitorV2UsageMetricsSQL, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL), day, end)
		exec(t, fmt.Sprintf(channelMonitorV2UserMetricsSQL, channelMonitorV2PlatformSQL, channelMonitorV2ModelSQL), day, end)
		exec(t, legacy, day, end)
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, repo.recomputeFixedRollups(ctx, tx, day, end))
		require.NoError(t, tx.Commit())
		exec(t, `UPDATE channel_monitor_v2_watermarks SET data_through=$1`, end)

		raw, err := os.ReadFile("../../migrations/362_channel_monitor_v2_local_session_blocks.sql")
		require.NoError(t, err)
		countRows := func(table, model string) int {
			t.Helper()
			var count int
			require.NoError(t, db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE model=$1", model).Scan(&count))
			return count
		}
		for _, suffix := range []string{"1m", "rollup"} {
			require.Positive(t, countRows("channel_monitor_v2_metrics_"+suffix, "foreign-model"))
		}
		for i := 0; i < 2; i++ {
			exec(t, string(raw))
			for _, kind := range []string{"metrics", "user_metrics", "error_metrics"} {
				for _, suffix := range []string{"1m", "rollup"} {
					table := "channel_monitor_v2_" + kind + "_" + suffix
					require.Zero(t, countRows(table, "foreign-model"), table)
					require.Positive(t, countRows(table, "mixed-model"), table)
					require.Positive(t, countRows(table, "provider-model"), table)
					if kind != "error_metrics" {
						for model, expected := range map[string]int{"mixed-model": 1, "two-403s": 2} {
							var errors int
							require.NoError(t, db.QueryRowContext(ctx, "SELECT max(error_requests) FROM "+table+" WHERE model=$1", model).Scan(&errors))
							require.Equal(t, expected, errors, table, model)
						}
					}
				}
			}
			require.Equal(t, 2, countRows("ops_error_logs", "foreign-model"))
		}
	})

	t.Run("small historical slices accumulate all daily metrics without double counting", func(t *testing.T) {
		reset(t)
		day := now.Add(-10 * 24 * time.Hour).Truncate(24 * time.Hour)
		exec(t, `UPDATE channel_monitor_v2_watermarks SET backfill_cursor=$1`, day.Add(24*time.Hour))
		exec(t, `INSERT INTO usage_logs(created_at,request_id) VALUES ($1::timestamptz+INTERVAL '23 hours 10 minutes','late'),($1::timestamptz+INTERVAL '22 hours 10 minutes','early')`, day)
		exec(t, `INSERT INTO ops_error_logs(created_at,request_id) VALUES ($1::timestamptz+INTERVAL '22 hours 20 minutes','failed')`, day)
		require.NoError(t, repo.RecomputeRange(ctx, day.Add(23*time.Hour), day.Add(24*time.Hour)))
		assertDaily(t, day, 1, 0)
		for i := 0; i < 2; i++ {
			require.NoError(t, repo.RecomputeRange(ctx, day.Add(22*time.Hour), day.Add(23*time.Hour)))
			assertDaily(t, day, 2, 1)
			var reads, input, histograms, errors int64
			require.NoError(t, db.QueryRowContext(ctx, `SELECT input_tokens,cache_read_tokens FROM channel_monitor_v2_metrics_rollup WHERE bucket_seconds=86400 AND bucket_start=$1`, day).Scan(&input, &reads))
			require.EqualValues(t, 20, input)
			require.EqualValues(t, 180, reads)
			require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(sample_count) FROM channel_monitor_v2_latency_histograms_rollup WHERE bucket_seconds=86400 AND bucket_start=$1 AND user_id=0 AND metric='ttft'`, day).Scan(&histograms))
			require.EqualValues(t, 2, histograms)
			require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(error_requests) FROM channel_monitor_v2_error_metrics_rollup WHERE bucket_seconds=86400 AND bucket_start=$1`, day).Scan(&errors))
			require.EqualValues(t, 1, errors)
		}
	})

	t.Run("a midnight tail refresh preserves the open daily bucket", func(t *testing.T) {
		reset(t)
		day := now.Truncate(24 * time.Hour)
		exec(t, `INSERT INTO channel_monitor_v2_metrics_rollup(bucket_start,bucket_seconds,platform,group_id,model,success_requests) VALUES ($1,86400,'openai',8,'model-a',7)`, day)
		exec(t, `INSERT INTO channel_monitor_v2_user_metrics_rollup(bucket_start,bucket_seconds,platform,group_id,model,user_id,success_requests) VALUES ($1,86400,'openai',8,'model-a',7,7)`, day)
		exec(t, `INSERT INTO usage_logs(created_at,request_id) VALUES ($1::timestamptz+INTERVAL '1 minute','midnight')`, day)
		require.NoError(t, repo.RecomputeRange(ctx, day, day.Add(10*time.Minute)))
		assertDaily(t, day, 7, 0)
		var count int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(success_requests) FROM channel_monitor_v2_metrics_1m`).Scan(&count))
		require.Equal(t, 1, count)
	})

	t.Run("finishing bootstrap keeps completed daily totals after minute facts expire", func(t *testing.T) {
		reset(t)
		cutoff := channelMonitorV2RetentionCutoff(now, channelMonitorV2RetentionMax)
		day := cutoff.Truncate(24 * time.Hour).Add(24 * time.Hour)
		exec(t, `UPDATE channel_monitor_v2_watermarks SET backfill_cursor=$1`, day.Add(24*time.Hour))
		exec(t, `INSERT INTO usage_logs(created_at,request_id) VALUES ($1::timestamptz+INTERVAL '23 hours 10 minutes','last-day-late'),($1::timestamptz+INTERVAL '22 hours 10 minutes','last-day-early')`, day)
		require.NoError(t, repo.RecomputeRange(ctx, day.Add(23*time.Hour), day.Add(24*time.Hour)))
		require.NoError(t, repo.RecomputeRange(ctx, day.Add(22*time.Hour), day.Add(23*time.Hour)))
		assertDaily(t, day, 2, 0)
		exec(t, `UPDATE channel_monitor_v2_watermarks SET backfill_cursor=$1`, cutoff.Add(15*time.Minute))
		require.NoError(t, repo.RecomputeRange(ctx, cutoff, cutoff.Add(15*time.Minute)))
		var facts int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_monitor_v2_metrics_1m WHERE bucket_start >= $1 AND bucket_start < $2`, day, day.Add(24*time.Hour)).Scan(&facts))
		require.Equal(t, 2, facts, "the final bootstrap transaction must retain source facts until its rollups commit")
		assertDaily(t, day, 2, 0)
		require.NoError(t, repo.RecomputeRange(ctx, now.Add(-10*time.Minute), now))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM channel_monitor_v2_metrics_1m WHERE bucket_start >= $1 AND bucket_start < $2`, day, day.Add(24*time.Hour)).Scan(&facts))
		require.Zero(t, facts)
		assertDaily(t, day, 2, 0)
		// Raw history may also have expired. A later partial replay must not
		// replace the only surviving complete aggregate with zero/partial data.
		exec(t, `DELETE FROM usage_logs WHERE created_at >= $1 AND created_at < $2`, day, day.Add(24*time.Hour))
		require.NoError(t, repo.RecomputeRange(ctx, day.Add(23*time.Hour), day.Add(24*time.Hour)))
		assertDaily(t, day, 2, 0)
	})
}
