//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestStatisticsIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	adminDB, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer adminDB.Close()
	schema := fmt.Sprintf("quality_stats_%d", time.Now().UnixNano())
	_, err = adminDB.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := adminDB.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	}()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	execSQL := func(statement string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, statement, args...)
		require.NoError(t, err)
	}
	execSQL(`CREATE TABLE accounts (id BIGINT PRIMARY KEY, status TEXT DEFAULT 'active', schedulable BOOLEAN DEFAULT true, deleted_at TIMESTAMPTZ);
CREATE TABLE account_groups (account_id BIGINT, group_id BIGINT);
CREATE TABLE usage_logs (
 id BIGSERIAL PRIMARY KEY, api_key_id BIGINT DEFAULT 1, request_id TEXT,
 group_id BIGINT DEFAULT 8, account_id BIGINT DEFAULT 62, created_at TIMESTAMPTZ NOT NULL,
 model TEXT DEFAULT 'model-a', requested_model TEXT, request_type SMALLINT DEFAULT 2,
 actual_cost NUMERIC DEFAULT 0, total_cost NUMERIC DEFAULT 0,
 input_tokens BIGINT DEFAULT 0, output_tokens BIGINT DEFAULT 0,
 cache_creation_tokens BIGINT DEFAULT 0, cache_read_tokens BIGINT DEFAULT 0,
 image_input_tokens BIGINT DEFAULT 0, image_output_tokens BIGINT DEFAULT 0,
 image_count BIGINT DEFAULT 0, video_count BIGINT DEFAULT 0,
 first_token_ms INTEGER, native_compaction_v2 BOOLEAN DEFAULT false,
 inbound_endpoint TEXT, upstream_endpoint TEXT
);
CREATE TABLE ops_error_logs (
 id BIGSERIAL PRIMARY KEY, api_key_id BIGINT DEFAULT 1, request_id TEXT, client_request_id TEXT,
 group_id BIGINT DEFAULT 8, account_id BIGINT DEFAULT 62, created_at TIMESTAMPTZ NOT NULL,
 model TEXT DEFAULT 'model-a', requested_model TEXT, request_type SMALLINT DEFAULT 2,
 status_code INTEGER DEFAULT 502, error_type TEXT DEFAULT 'upstream_error', is_count_tokens BOOLEAN DEFAULT false
);`)
	end := time.Date(2026, 9, 21, 12, 0, 0, 0, time.UTC)
	groupID, accountID := int64(8), int64(62)
	filter := service.ScheduledTestStatisticsFilter{GroupID: &groupID, Model: "model-a", WindowStart: end.Add(-time.Hour), WindowEnd: end}
	repo := &scheduledTestResultRepository{db: db}
	insert := func(table string, values map[string]any) {
		t.Helper()
		if _, ok := values["created_at"]; !ok {
			values["created_at"] = end.Add(-30 * time.Minute)
		}
		columns := make([]string, 0, len(values))
		for column := range values {
			columns = append(columns, column)
		}
		sort.Strings(columns)
		args, quoted, placeholders := make([]any, 0, len(columns)), make([]string, 0, len(columns)), make([]string, 0, len(columns))
		for _, column := range columns {
			args = append(args, values[column])
			quoted = append(quoted, pq.QuoteIdentifier(column))
			placeholders = append(placeholders, fmt.Sprintf("$%d", len(args)))
		}
		execSQL("INSERT INTO "+pq.QuoteIdentifier(table)+" ("+strings.Join(quoted, ",")+") VALUES ("+strings.Join(placeholders, ",")+")", args...)
	}
	usage := func(values map[string]any) { t.Helper(); insert("usage_logs", values) }
	failure := func(values map[string]any) { t.Helper(); insert("ops_error_logs", values) }
	reset := func() { execSQL("TRUNCATE usage_logs, ops_error_logs RESTART IDENTITY") }
	collect := func(f service.ScheduledTestStatisticsFilter) *service.ScheduledTestStatistics {
		t.Helper()
		result, err := repo.CollectStatistics(ctx, f)
		require.NoError(t, err)
		return result
	}

	t.Run("no recorded requests has no invented percentages", func(t *testing.T) {
		reset()
		result := collect(filter)
		require.Zero(t, result.TotalRequests)
		require.NotNil(t, result.RecentRequests)
		require.Empty(t, result.RecentRequests)
		require.Nil(t, result.SuccessRate)
		require.Nil(t, result.CacheRate)
		require.Nil(t, result.AvgFirstTokenMs)
		require.Equal(t, filter.WindowStart, result.WindowStart)
		require.Equal(t, filter.WindowEnd, result.WindowEnd)
	})
	t.Run("weighted cache and free successes exclude blocked placeholders and compaction cache", func(t *testing.T) {
		reset()
		usage(map[string]any{"input_tokens": 100, "image_input_tokens": 20, "cache_read_tokens": 30, "cache_creation_tokens": 10, "first_token_ms": 100})
		usage(map[string]any{"input_tokens": 40, "cache_creation_tokens": 20, "first_token_ms": 300})
		usage(map[string]any{"input_tokens": 1000, "cache_read_tokens": 900, "native_compaction_v2": true})
		usage(map[string]any{"input_tokens": 1000, "cache_read_tokens": 900, "inbound_endpoint": "/v1/responses/compact", "first_token_ms": -1})
		usage(map[string]any{"input_tokens": 1000, "upstream_endpoint": "/v1/responses/compact"})
		usage(map[string]any{})
		usage(map[string]any{"request_type": 4, "actual_cost": 2, "input_tokens": 100})
		usage(map[string]any{"request_type": 6, "actual_cost": 2, "input_tokens": 100})
		result := collect(filter)
		require.EqualValues(t, 5, result.SuccessRequests)
		require.EqualValues(t, 0, result.FailedRequests)
		require.EqualValues(t, 30, result.CacheReadTokens)
		require.EqualValues(t, 180, result.CacheInputTokens)
		require.InDelta(t, 1.0/6, *result.CacheRate, 0.00001)
		require.EqualValues(t, 2, result.FirstTokenSamples)
		require.InDelta(t, 200, *result.AvgFirstTokenMs, 0.001)
	})
	t.Run("terminal failures override billed partial output across stored request ID formats", func(t *testing.T) {
		reset()
		for _, key := range []string{"local:one", "client:two", "three", "four-client"} {
			usage(map[string]any{"request_id": key, "actual_cost": 1, "input_tokens": 50, "first_token_ms": 10})
		}
		failure(map[string]any{"request_id": "one"})
		failure(map[string]any{"request_id": "one"}) // duplicated final log
		failure(map[string]any{"request_id": "two-local", "client_request_id": "two"})
		failure(map[string]any{"request_id": "three"})
		failure(map[string]any{"request_id": "four", "client_request_id": "four-client"})
		usage(map[string]any{"request_id": "local:one", "api_key_id": 2, "input_tokens": 20})
		usage(map[string]any{"request_id": "local:recovered", "input_tokens": 30})
		failure(map[string]any{"request_id": "recovered", "status_code": 200})
		failure(map[string]any{"request_id": "token-probe", "is_count_tokens": true})
		result := collect(filter)
		require.EqualValues(t, 2, result.SuccessRequests)
		require.EqualValues(t, 4, result.FailedRequests)
		require.EqualValues(t, 6, result.TotalRequests)
		require.Len(t, result.RecentRequests, 6)
		var recentFailures int
		for _, request := range result.RecentRequests {
			if !request.Success {
				recentFailures++
			}
		}
		require.Equal(t, 4, recentFailures, "billed failures and duplicated final logs appear only once")
		require.InDelta(t, 1.0/3, *result.SuccessRate, 0.00001)
		require.EqualValues(t, 50, result.CacheInputTokens, "partial failures do not enter successful cache metrics")
		require.Nil(t, result.AvgFirstTokenMs)
	})
	t.Run("websocket failed turns sharing one connection stay separate", func(t *testing.T) {
		reset()
		failure(map[string]any{"request_id": "ws-connection", "request_type": 3})
		failure(map[string]any{"request_id": "ws-connection", "request_type": 3})
		usage(map[string]any{"request_id": "ws-connection", "request_type": 3, "input_tokens": 100})
		result := collect(filter)
		require.EqualValues(t, 1, result.SuccessRequests)
		require.EqualValues(t, 2, result.FailedRequests)
	})
	t.Run("deduplicate request identities without collapsing anonymous requests", func(t *testing.T) {
		reset()
		usage(map[string]any{"request_id": "same", "input_tokens": 10})
		usage(map[string]any{"request_id": "same", "input_tokens": 20})
		usage(map[string]any{"request_id": "", "input_tokens": 30})
		usage(map[string]any{"input_tokens": 40})
		failure(map[string]any{"client_request_id": "client", "request_id": "attempt-one"})
		failure(map[string]any{"client_request_id": "client", "request_id": "attempt-two"})
		failure(map[string]any{})
		failure(map[string]any{})
		failure(map[string]any{"status_code": 200, "error_type": "cyber_policy"})
		result := collect(filter)
		require.EqualValues(t, 3, result.SuccessRequests)
		require.EqualValues(t, 4, result.FailedRequests)
		require.EqualValues(t, 90, result.CacheInputTokens)
	})
	t.Run("scope follows requested model and half open time window", func(t *testing.T) {
		reset()
		usage(map[string]any{"created_at": filter.WindowStart, "model": "routed-model", "requested_model": " model-a ", "input_tokens": 10})
		failure(map[string]any{"model": "routed-model", "requested_model": "model-a"})
		usage(map[string]any{"account_id": 63, "input_tokens": 10, "requested_model": " "})
		failure(map[string]any{"account_id": 63})
		for _, values := range []map[string]any{
			{"created_at": filter.WindowStart.Add(-time.Second)}, {"created_at": end},
			{"group_id": 9}, {"requested_model": "other-model"}, {"model": "other-model"},
		} {
			failure(values)
			values["input_tokens"] = 10
			usage(values)
		}
		result := collect(filter)
		require.EqualValues(t, 2, result.SuccessRequests)
		require.EqualValues(t, 2, result.FailedRequests)
		require.Len(t, result.RecentRequests, 4)
		accountFilter := filter
		accountFilter.AccountID = &accountID
		result = collect(accountFilter)
		require.EqualValues(t, 1, result.SuccessRequests)
		require.EqualValues(t, 1, result.FailedRequests)
		require.Len(t, result.RecentRequests, 2)
		accountFilter.GroupID = nil
		result = collect(accountFilter)
		require.EqualValues(t, 2, result.SuccessRequests)
		require.EqualValues(t, 2, result.FailedRequests)
	})
	t.Run("recent outcomes keep newest ten with failure precedence and exact scope", func(t *testing.T) {
		reset()
		for i := 0; i < 12; i++ {
			key := fmt.Sprintf("recent-%d", i)
			at := end.Add(-time.Duration(12-i) * time.Minute)
			usage(map[string]any{"request_id": "local:" + key, "input_tokens": 20, "created_at": at})
			if i%2 == 1 {
				failure(map[string]any{"request_id": key, "created_at": at})
				failure(map[string]any{"request_id": key, "created_at": at})
			}
		}
		// These newer rows must not displace the selected account/model/window.
		for _, values := range []map[string]any{
			{"account_id": 63}, {"group_id": 9}, {"requested_model": "other-model"},
			{"created_at": end}, {"is_count_tokens": true}, {"status_code": 200},
		} {
			if _, ok := values["created_at"]; !ok {
				values["created_at"] = end.Add(-time.Second)
			}
			failure(values)
		}
		scoped := filter
		scoped.AccountID = &accountID
		result := collect(scoped)
		require.EqualValues(t, 12, result.TotalRequests)
		require.Len(t, result.RecentRequests, 10)
		for index, request := range result.RecentRequests {
			require.WithinDuration(t, end.Add(-time.Duration(index+1)*time.Minute), request.CreatedAt, 0)
			require.Equal(t, index%2 == 1, request.Success, "the newest outcome is a failed request")
		}
	})
	t.Run("recent failures are shown even with no successful usage", func(t *testing.T) {
		reset()
		for i := 0; i < 3; i++ {
			failure(map[string]any{"created_at": end.Add(-time.Duration(i+1) * time.Minute)})
		}
		result := collect(filter)
		require.Zero(t, result.SuccessRequests)
		require.Len(t, result.RecentRequests, 3)
		for index, request := range result.RecentRequests {
			require.False(t, request.Success)
			require.WithinDuration(t, end.Add(-time.Duration(index+1)*time.Minute), request.CreatedAt, 0)
		}
	})
	t.Run("same timestamp has deterministic internal row ordering", func(t *testing.T) {
		reset()
		usage(map[string]any{"id": 1, "input_tokens": 20})
		failure(map[string]any{"id": 50})
		usage(map[string]any{"id": 100, "input_tokens": 20})
		result := collect(filter)
		require.Len(t, result.RecentRequests, 3)
		require.True(t, result.RecentRequests[0].Success)
		require.False(t, result.RecentRequests[1].Success)
		require.True(t, result.RecentRequests[2].Success)
	})
	t.Run("all account snapshots include disabled and unschedulable accounts", func(t *testing.T) {
		execSQL(`INSERT INTO accounts (id,status,schedulable,deleted_at) VALUES
 (62,'active',true,NULL), (63,'disabled',false,NULL), (64,'active',false,NULL), (65,'active',true,NOW()), (66,'active',true,NULL);
INSERT INTO account_groups VALUES (62,8),(63,8),(64,8),(65,8),(66,9)`)
		ids, err := repo.ListStatisticsAccountIDs(ctx, groupID)
		require.NoError(t, err)
		require.Equal(t, []int64{62, 63, 64}, ids)
	})
	t.Run("unbounded or unscoped query is rejected", func(t *testing.T) {
		for _, invalid := range []service.ScheduledTestStatisticsFilter{
			{}, {Model: "model-a", WindowStart: filter.WindowStart, WindowEnd: end},
			{GroupID: &groupID, Model: "model-a", WindowStart: end, WindowEnd: filter.WindowStart},
		} {
			_, err := repo.CollectStatistics(ctx, invalid)
			require.Error(t, err)
		}
	})
}
