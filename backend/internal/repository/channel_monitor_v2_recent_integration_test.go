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

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestChannelMonitorV2RecentRequestsIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("channel_recent_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
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
	_, err = db.ExecContext(ctx, `
CREATE TABLE groups (id bigint PRIMARY KEY, platform text);
CREATE TABLE accounts (id bigint PRIMARY KEY, platform text);
CREATE TABLE usage_logs (id bigserial PRIMARY KEY, created_at timestamptz, group_id bigint, account_id bigint DEFAULT 1, request_id text, model text, requested_model text, request_type int DEFAULT 2, actual_cost numeric DEFAULT 1, first_token_ms int DEFAULT 1000,
 duration_ms int DEFAULT 3500, input_tokens int DEFAULT 100, cache_creation_tokens int DEFAULT 100, cache_read_tokens int DEFAULT 800, stream boolean DEFAULT true, openai_ws_mode boolean DEFAULT false);
CREATE TABLE ops_error_logs (id bigserial PRIMARY KEY, created_at timestamptz, group_id bigint, account_id bigint DEFAULT 1, request_id text, platform text DEFAULT 'openai', model text DEFAULT 'gpt-named', requested_model text, is_count_tokens boolean DEFAULT false, status_code int DEFAULT 503, upstream_status_code int, error_type text DEFAULT 'upstream', error_owner text, error_source text, error_message text, upstream_error_message text, upstream_error_detail text, error_body text,
 duration_ms int, time_to_first_token_ms bigint);
INSERT INTO groups VALUES(7,'openai'),(8,'openai'),(9,'composite');
INSERT INTO accounts VALUES(1,'openai');
INSERT INTO usage_logs(created_at,group_id,request_id,model) SELECT '2026-10-20T00:00:00Z'::timestamptz + n*interval '1 minute',7,'seven-'||n,'gpt-named' FROM generate_series(1,25) n;
INSERT INTO usage_logs(created_at,group_id,request_id,model) SELECT '2026-10-20T00:00:00Z'::timestamptz + n*interval '1 minute',8,'eight-'||n,'private-model' FROM generate_series(1,45) n;
INSERT INTO usage_logs(created_at,group_id,request_id,model) VALUES
 ('2026-10-20T00:26:00Z',7,'duplicate','gpt-named'),('2026-10-20T00:27:00Z',7,'duplicate','gpt-named'),
 ('2026-10-20T00:29:00Z',7,'recovered','gpt-named'),('2026-10-20T00:30:00Z',7,'unknown-model','actual-unnamed'),
 ('2026-10-20T00:30:00Z',9,'composite-success','gpt-named'),('2026-10-20T01:01:00Z',7,'outside-window','gpt-named');
INSERT INTO usage_logs(created_at,group_id,request_id,model,request_type,actual_cost) VALUES
 ('2026-10-20T00:50:00Z',7,'cyber','gpt-named',4,1),('2026-10-20T00:51:00Z',7,'count','gpt-named',6,1),('2026-10-20T00:52:00Z',7,'unbilled','gpt-named',2,0);
INSERT INTO ops_error_logs(created_at,group_id,request_id,status_code,error_type) VALUES
 ('2026-10-20T00:27:00Z',7,'recovered',503,'recovered-attempt'),
 ('2026-10-20T00:31:00Z',7,'final-error',503,'old-error'),('2026-10-20T00:32:00Z',7,'final-error',503,'latest-error'),
 ('2026-10-20T00:33:00Z',7,'ignored',401,'authentication'),
 ('2026-10-20T00:53:00Z',7,'attempt-only',0,'attempt-only'),
 ('2026-10-20T00:34:00Z',9,'composite-error',503,'composite-error');
UPDATE ops_error_logs SET platform='composite' WHERE group_id=9;
INSERT INTO ops_error_logs(created_at,group_id,request_id,is_count_tokens,error_type) VALUES('2026-10-20T00:54:00Z',7,'token-count',true,'count-token-error');
UPDATE usage_logs SET input_tokens=0, cache_creation_tokens=0, cache_read_tokens=0 WHERE request_id='seven-20';
UPDATE usage_logs SET cache_read_tokens=0, first_token_ms=0, duration_ms=0 WHERE request_id='seven-21';
UPDATE usage_logs SET request_type=1, stream=false WHERE request_id='seven-22';
UPDATE usage_logs SET request_type=0, stream=false WHERE request_id='seven-23';
UPDATE usage_logs SET request_type=3, stream=false, openai_ws_mode=true WHERE request_id='seven-24';
UPDATE usage_logs SET first_token_ms=NULL, duration_ms=NULL WHERE request_id='seven-25';
UPDATE ops_error_logs SET duration_ms=5000, time_to_first_token_ms=400 WHERE request_id='final-error';
UPDATE ops_error_logs SET error_message='Internal server error', upstream_status_code=502,
 upstream_error_message='Upstream worker failed: trace=test-trace Bearer private-token', upstream_error_detail='fallback detail'
 WHERE error_type='latest-error';
UPDATE ops_error_logs SET upstream_error_detail='Credential expired', error_message='Request failed' WHERE request_id='ignored';
`)
	require.NoError(t, err)
	start := time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)
	seven, eight, nine := int64(7), int64(8), int64(9)
	cfg := service.ChannelMonitorV2Config{Platforms: []service.ChannelMonitorV2PlatformConfig{{Platform: "openai", Enabled: true, Models: []string{"gpt-named"}}}, HealthThresholds: service.DefaultChannelMonitorV2HealthThresholds(), IgnoredErrorCategories: []string{"authentication"}}
	filter := service.ChannelMonitorV2Filter{Start: start, End: start.Add(time.Hour), RestrictGroups: true, AllowedGroupIDs: []int64{7}}
	repo := &channelMonitorV2Repository{db: db}
	for _, groupID := range []*int64{&seven, nil} {
		items := []service.ChannelMonitorV2MatrixRow{{Platform: "openai", GroupID: groupID}}
		require.NoError(t, repo.loadChannelMonitorV2RecentRequests(ctx, filter, cfg, items, false))
		recent := items[0].RecentRequests
		require.Len(t, recent, 20)
		errors, ignored, unnamed := 0, 0, 0
		for i, request := range recent {
			require.Nil(t, request.Error)
			require.Empty(t, request.ErrorCategory)
			if i > 0 {
				require.False(t, request.OccurredAt.Before(recent[i-1].OccurredAt))
			}
			require.NotEqual(t, start.Add(26*time.Minute), request.OccurredAt) // duplicate source row
			if request.Status == "error" && request.OccurredAt.Equal(start.Add(32*time.Minute)) {
				errors++
				require.EqualValues(t, 5000, *request.DurationMs)
				require.EqualValues(t, 400, *request.TTFTMs)
				require.Nil(t, request.CacheRate)
			}
			if request.Status == "error" && request.OccurredAt.Equal(start.Add(33*time.Minute)) {
				ignored++
				require.Nil(t, request.DurationMs)
				require.Nil(t, request.TTFTMs)
				require.Nil(t, request.CacheRate)
			}
			if request.Status == "success" {
				switch request.OccurredAt.Sub(start) {
				case 20 * time.Minute, 22 * time.Minute, 23 * time.Minute:
					require.Nil(t, request.CacheRate) // absent input or synchronous cache telemetry
				case 21 * time.Minute:
					require.Zero(t, *request.CacheRate) // known zero is distinct from missing data
					require.Zero(t, *request.TTFTMs)
					require.Zero(t, *request.DurationMs)
				default:
					require.InDelta(t, 0.8, *request.CacheRate, 0.00001)
				}
				if request.OccurredAt.Equal(start.Add(25 * time.Minute)) {
					require.Nil(t, request.TTFTMs)
					require.Nil(t, request.DurationMs)
				}
			}
			if request.Model == "actual-unnamed" {
				unnamed++
			}
		}
		require.Equal(t, 1, errors)
		require.Equal(t, 1, ignored) // ignored categories are indistinguishable from generic failures to users
		require.Equal(t, 1, unnamed) // unauthorized group's 45 requests do not leak into platform view
		require.Equal(t, start.Add(33*time.Minute), recent[19].OccurredAt)
	}
	filter.RestrictGroups = false
	items := []service.ChannelMonitorV2MatrixRow{{Platform: "openai", GroupID: &seven}, {Platform: "openai", GroupID: &eight}, {Platform: "openai", GroupID: &nine}}
	require.NoError(t, repo.loadChannelMonitorV2RecentRequests(ctx, filter, cfg, items, true))
	require.Len(t, items[0].RecentRequests, 20)
	require.Len(t, items[1].RecentRequests, 20)
	require.Len(t, items[2].RecentRequests, 2)
	require.Equal(t, "error", items[2].RecentRequests[1].Status)
	for _, request := range items[0].RecentRequests {
		switch request.Status {
		case "error":
			require.NotNil(t, request.Error)
			require.Equal(t, "upstream_5xx", request.ErrorCategory)
			require.Equal(t, "latest-error", request.Error.ErrorType)
			require.Equal(t, 503, request.Error.StatusCode)
			require.Equal(t, 502, request.Error.UpstreamStatusCode)
			require.Equal(t, "Upstream worker failed: trace=test-trace Bearer ****", request.Error.Message)
			var errorType string
			require.NoError(t, db.QueryRowContext(ctx, "SELECT error_type FROM ops_error_logs WHERE id=$1", request.Error.LogID).Scan(&errorType))
			require.Equal(t, "latest-error", errorType)
		case "ignored":
			require.NotNil(t, request.Error)
			require.Equal(t, "authentication", request.ErrorCategory)
			require.Equal(t, "Credential expired", request.Error.Message)
		default:
			require.Nil(t, request.Error)
		}
	}
	require.Empty(t, items[2].RecentRequests[1].Error.Message) // No invented cause for a missing message.
	filter.RestrictGroups = true
	filter.Models = []string{"actual-unnamed"}
	items = []service.ChannelMonitorV2MatrixRow{{Platform: "openai", GroupID: &seven}}
	require.NoError(t, repo.loadChannelMonitorV2RecentRequests(ctx, filter, cfg, items, false))
	require.Len(t, items[0].RecentRequests, 1)
	require.Equal(t, "actual-unnamed", items[0].RecentRequests[0].Model)
}
