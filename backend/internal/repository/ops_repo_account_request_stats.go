package repository

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// This aggregation deliberately has no request-count limit: the independently
// paginated request timeline must not truncate the one-hour account metrics.
// Deduplicate terminal errors by account and API key, then let a terminal error
// override a billed partial response. WebSocket connection errors do not erase
// completed turns. Free responses with actual tokens remain successful.
const opsAccountRequestStatsSQL = `WITH errors AS MATERIALIZED (
 SELECT DISTINCT ON (e.account_id, COALESCE(e.api_key_id,0), COALESCE(NULLIF(e.request_id,''),NULLIF(e.client_request_id,''),'error:'||e.id::text))
 e.id,e.account_id,e.api_key_id,e.request_id,e.client_request_id,e.request_type,e.created_at FROM ops_error_logs e
 WHERE e.created_at >= $1 AND e.created_at < $2 AND e.account_id=ANY($3)
 AND NOT COALESCE(e.is_count_tokens,false) AND COALESCE(e.request_type,0) NOT IN (4,6)
 AND COALESCE(e.status_code,0)>=400
 ORDER BY e.account_id, COALESCE(e.api_key_id,0), COALESCE(NULLIF(e.request_id,''),NULLIF(e.client_request_id,''),'error:'||e.id::text), e.created_at DESC,e.id DESC
), usage AS MATERIALIZED (
 SELECT DISTINCT ON (ul.account_id,COALESCE(ul.api_key_id,0),COALESCE(NULLIF(ul.request_id,''),'usage:'||ul.id::text))
 ul.account_id,ul.cache_read_tokens,ul.first_token_ms,ul.duration_ms,ul.input_tokens,
 ul.image_input_tokens,ul.cache_creation_tokens,ul.request_type,ul.stream,ul.openai_ws_mode
 FROM usage_logs ul
 WHERE ul.created_at >= $1 AND ul.created_at < $2 AND ul.account_id=ANY($3)
 AND COALESCE(ul.request_type,0) NOT IN (4,6)
 AND (ul.actual_cost>0 OR ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_read_tokens>0 OR ul.cache_creation_tokens>0)
 AND (COALESCE(ul.request_type,0)=3 OR ul.openai_ws_mode=TRUE OR NOT EXISTS (
  SELECT 1 FROM errors e WHERE e.account_id=ul.account_id AND COALESCE(e.api_key_id,0)=COALESCE(ul.api_key_id,0)
  AND COALESCE(e.request_type,0)<>3 AND NULLIF(ul.request_id,'') IN (NULLIF(e.request_id,''),NULLIF(e.client_request_id,''),'local:'||NULLIF(e.request_id,''),'client:'||NULLIF(e.client_request_id,''))
 ))
 ORDER BY ul.account_id,COALESCE(ul.api_key_id,0),COALESCE(NULLIF(ul.request_id,''),'usage:'||ul.id::text),ul.created_at DESC,ul.id DESC
), measurements AS (
 SELECT ul.account_id, ul.cache_read_tokens, ul.first_token_ms, ul.duration_ms,
   GREATEST(ul.input_tokens-COALESCE(ul.image_input_tokens,0),0)::numeric
     +ul.cache_creation_tokens+ul.cache_read_tokens AS prompt_tokens,
   (COALESCE(ul.request_type,0) NOT IN (1,4,6)
     AND (ul.stream=TRUE OR ul.openai_ws_mode=TRUE OR ul.request_type=3)
     AND ul.input_tokens>=0 AND ul.cache_read_tokens>=0 AND ul.cache_creation_tokens>=0
     AND (ul.image_input_tokens IS NULL OR ul.image_input_tokens>=0)) AS cache_eligible,
   (CASE WHEN ul.request_type IN (1,2,3,4,5,6) THEN ul.request_type
     WHEN ul.openai_ws_mode=TRUE THEN 3 WHEN ul.stream=TRUE THEN 2 ELSE 1 END
     IN (2,3)) AS first_token_eligible
 FROM usage ul
), aggregates AS (
 SELECT account_id,
 (SUM(cache_read_tokens) FILTER (WHERE cache_eligible AND prompt_tokens>0))::float8
   / NULLIF(SUM(prompt_tokens) FILTER (WHERE cache_eligible AND prompt_tokens>0),0)::float8 AS cache_rate,
 (AVG(first_token_ms) FILTER (WHERE first_token_eligible AND first_token_ms>=0))::float8 AS avg_first_token_ms,
 (AVG(duration_ms) FILTER (WHERE duration_ms>=0))::float8 AS avg_duration_ms,
 COUNT(*) FILTER (WHERE cache_eligible AND prompt_tokens>0) AS cache_samples,
 COUNT(*) FILTER (WHERE first_token_eligible AND first_token_ms>=0) AS first_token_samples,
 COUNT(*) FILTER (WHERE duration_ms>=0) AS duration_samples,
 COUNT(*) AS successes
 FROM measurements GROUP BY account_id
), failures AS (SELECT account_id,COUNT(*) AS failures FROM errors GROUP BY account_id)
SELECT COALESCE(a.account_id,f.account_id),a.cache_rate,a.avg_first_token_ms,a.avg_duration_ms,
 COALESCE(a.cache_samples,0),COALESCE(a.first_token_samples,0),COALESCE(a.duration_samples,0),
 COALESCE(a.successes,0)::float8 / NULLIF(COALESCE(a.successes,0)+COALESCE(f.failures,0),0),
 COALESCE(a.successes,0),COALESCE(f.failures,0),COALESCE(a.successes,0)+COALESCE(f.failures,0)
FROM aggregates a FULL JOIN failures f ON a.account_id=f.account_id`

func (r *opsRepository) GetAccountRequestStats(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[string]*service.OpsAccountRequestStats, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	return readOpsAccountRequestStats(ctx, r.db, accountIDs, startTime, endTime)
}

func readOpsAccountRequestStats(ctx context.Context, db sqlExecutor, accountIDs []int64, startTime, endTime time.Time) (map[string]*service.OpsAccountRequestStats, error) {
	stats := make(map[string]*service.OpsAccountRequestStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return stats, nil
	}
	rows, err := db.QueryContext(ctx, opsAccountRequestStatsSQL, startTime.UTC(), endTime.UTC(), pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accountID int64
		entry := &service.OpsAccountRequestStats{WindowStart: startTime, WindowEnd: endTime}
		if err := rows.Scan(&accountID, &entry.CacheRate, &entry.AvgFirstTokenMs, &entry.AvgDurationMs,
			&entry.CacheSamples, &entry.FirstTokenSamples, &entry.DurationSamples, &entry.SuccessRate, &entry.SuccessRequests, &entry.ErrorRequests, &entry.RequestSamples); err != nil {
			return nil, err
		}
		stats[strconv.FormatInt(accountID, 10)] = entry
	}
	return stats, rows.Err()
}
