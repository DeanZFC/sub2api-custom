package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// A billed partial response can still fail, and a free response can succeed.
// Use terminal request outcomes independently of the legacy billing metrics.
// WS error IDs identify connections, so they cannot invalidate successful turns.
var channelMonitorV2FinalFailureKeysSQL = `WITH tps_final_failure_keys AS MATERIALIZED (
 SELECT DISTINCT COALESCE(e.api_key_id,0) AS api_key_id,COALESCE(e.account_id,0) AS account_id,keys.request_id
 FROM ops_error_logs e CROSS JOIN LATERAL unnest(ARRAY[
  'client:'||NULLIF(TRIM(e.client_request_id),''), 'local:'||NULLIF(TRIM(e.request_id),''),
  NULLIF(TRIM(e.request_id),''), NULLIF(TRIM(e.client_request_id),'')
 ]) keys(request_id)
 WHERE e.created_at >= $1 AND e.created_at < $2
 AND NOT COALESCE(e.is_count_tokens,false) AND COALESCE(e.request_type,0)<>3
 AND ` + channelMonitorV2FailureSQL("e") + `
 AND keys.request_id IS NOT NULL
)
`

// HTTP errors identify requests. WS errors only identify the connection and
// must not invalidate other successful turns on it.
const channelMonitorV2UsageFailureJoinSQL = `LEFT JOIN tps_final_failure_keys e
 ON e.api_key_id=COALESCE(ul.api_key_id,0) AND e.account_id=COALESCE(ul.account_id,0)
 AND e.request_id=NULLIF(TRIM(ul.request_id),'')
 AND COALESCE(ul.request_type,0)<>3 AND NOT COALESCE(ul.openai_ws_mode,false)`

// The monitor reports text responses. Keep its exclusions for generated media,
// compaction and policy-block records when applying the Ops rate to channel traffic.
var channelMonitorV2OutputTPSEligibleSQL = usageOutputTPSEligibleSQL + `
 AND (ul.api_key_id IS NOT NULL OR ul.http_status BETWEEN 200 AND 299)
 AND ul.request_type <> 4 AND COALESCE(ul.video_count,0)=0
 AND COALESCE(ul.inbound_endpoint,'') !~* '(^|/)(images|videos|audio|compact)(/|$)'
 AND COALESCE(ul.upstream_endpoint,'') !~* '(^|/)(images|videos|audio|compact)(/|$)'
 AND NOT COALESCE(ul.native_compaction_v2,false)`

const channelMonitorV2GroupByUser service.ChannelMonitorV2GroupBy = "user"

type channelMonitorV2RateKey struct {
	channelMonitorV2MatrixKey
	userID int64
	bucket string // empty for the entire window
}

type channelMonitorV2Rate struct {
	p50     *float64
	samples int64
}

func (r channelMonitorV2Rate) apply(m *service.ChannelMonitorV2Metric) {
	m.OutputTPS, m.TPSSamples = r.p50, r.samples
}

// A percentile cannot be recovered from the legacy token and time sums.
// Compute both the full-window and bucket medians from retained usage records.
func (r *channelMonitorV2Repository) loadOutputTPS(ctx context.Context, filter service.ChannelMonitorV2Filter, cfg service.ChannelMonitorV2Config, groupBy service.ChannelMonitorV2GroupBy, byBucket bool) (map[channelMonitorV2RateKey]channelMonitorV2Rate, error) {
	if !filter.Start.Before(filter.End) {
		return nil, nil
	}
	where, args := channelMonitorV2Where(filter, cfg, "m")
	if len(filter.Models) > 0 {
		args = append(args, pq.Array(filter.Models))
		where += fmt.Sprintf(" AND m.model = ANY($%d)", len(args))
	}

	platform, group, model, user := "''::text", "0::bigint", "''::text", "0::bigint"
	switch groupBy {
	case service.ChannelMonitorV2GroupByPlatform:
		platform = "m.platform"
	case service.ChannelMonitorV2GroupByPlatformGroup:
		platform, group = "m.platform", "visible.group_id"
	case service.ChannelMonitorV2GroupByPlatformModel:
		platform, model = "m.platform", "m.model"
	case service.ChannelMonitorV2GroupByPlatformGroupModel:
		platform, group, model = "m.platform", "visible.group_id", "m.model"
	case channelMonitorV2GroupByUser:
		user = "m.user_id"
		where += " AND m.user_id IS NOT NULL"
	}

	bucket, grouping := "NULL::timestamptz", "platform_key, group_key, model_key, user_key"
	if byBucket {
		args = append(args, fmt.Sprintf("%d seconds", int(filter.Bucket.Seconds())))
		bucket = fmt.Sprintf("date_bin($%d::interval, m.bucket_start, %s)", len(args), channelMonitorV2DateBinOrigin)
		grouping += ", ROLLUP(bucket)"
	}
	_, join := channelMonitorV2ProjectGroups("m.group_id", channelMonitorV2GroupsInDimension(groupBy), filter, cfg, &args)
	query := channelMonitorV2FinalFailureKeysSQL + `
 SELECT ` + platform + ` AS platform_key, ` + group + ` AS group_key, ` + model + ` AS model_key, ` + user + ` AS user_key,
        ` + bucket + ` AS bucket, percentile_cont(0.5) WITHIN GROUP (ORDER BY m.rate), COUNT(*)
 FROM (
   SELECT ul.created_at AS bucket_start, ` + channelMonitorV2PlatformSQL + ` AS platform, ` + channelMonitorV2UsageGroup + ` AS group_id,
          ` + channelMonitorV2ModelSQL + ` AS model, ul.user_id, ` + usageOutputTPSSQL + ` AS rate
   FROM usage_logs ul
   LEFT JOIN groups g ON g.id = ul.group_id
   LEFT JOIN accounts a ON a.id = ul.account_id
   ` + channelMonitorV2UsageFailureJoinSQL + `
   WHERE e.request_id IS NULL AND ` + channelMonitorV2OutputTPSEligibleSQL + `
 ) m ` + join + where + ` GROUP BY ` + grouping

	rateCtx, cancel := context.WithTimeout(ctx, opsRawLatencyQueryTimeout)
	defer cancel()
	rates, err := r.queryChannelOutputTPS(rateCtx, query, args)
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if err != nil && (errors.Is(rateCtx.Err(), context.DeadlineExceeded) || isQueryTimeoutErr(err)) {
		return nil, nil // Retain the other channel metrics when raw percentiles time out.
	}
	return rates, err
}

func (r *channelMonitorV2Repository) queryChannelOutputTPS(ctx context.Context, query string, args []any) (map[channelMonitorV2RateKey]channelMonitorV2Rate, error) {
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rates := make(map[channelMonitorV2RateKey]channelMonitorV2Rate)
	for rows.Next() {
		var key channelMonitorV2RateKey
		var rate channelMonitorV2Rate
		var bucket sql.NullTime
		if err := rows.Scan(&key.platform, &key.groupID, &key.model, &key.userID, &bucket, &rate.p50, &rate.samples); err != nil {
			return nil, err
		}
		if bucket.Valid {
			key.bucket = bucket.Time.UTC().Format(time.RFC3339Nano)
		}
		rates[key] = rate
	}
	return rates, rows.Err()
}
