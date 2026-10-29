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
const opsAccountRequestStatsSQL = `WITH measurements AS (
 SELECT ul.account_id, ul.cache_read_tokens, ul.first_token_ms, ul.duration_ms,
   GREATEST(ul.input_tokens-COALESCE(ul.image_input_tokens,0),0)::numeric
     +ul.cache_creation_tokens+ul.cache_read_tokens AS prompt_tokens,
   (COALESCE(ul.request_type,0) NOT IN (1,4)
     AND (ul.stream=TRUE OR ul.openai_ws_mode=TRUE OR ul.request_type=3)
     AND ul.input_tokens>=0 AND ul.cache_read_tokens>=0 AND ul.cache_creation_tokens>=0
     AND (ul.image_input_tokens IS NULL OR ul.image_input_tokens>=0)) AS cache_eligible,
   (CASE WHEN ul.request_type IN (1,2,3,4,5) THEN ul.request_type
     WHEN ul.openai_ws_mode=TRUE THEN 3 WHEN ul.stream=TRUE THEN 2 ELSE 1 END
     IN (2,3)) AS first_token_eligible
 FROM usage_logs ul
 WHERE ul.created_at >= $1 AND ul.created_at < $2 AND ul.account_id=ANY($3)
)
SELECT account_id,
 (SUM(cache_read_tokens) FILTER (WHERE cache_eligible AND prompt_tokens>0))::float8
   / NULLIF(SUM(prompt_tokens) FILTER (WHERE cache_eligible AND prompt_tokens>0),0)::float8 AS cache_rate,
 (AVG(first_token_ms) FILTER (WHERE first_token_eligible AND first_token_ms>=0))::float8 AS avg_first_token_ms,
 (AVG(duration_ms) FILTER (WHERE duration_ms>=0))::float8 AS avg_duration_ms,
 COUNT(*) FILTER (WHERE cache_eligible AND prompt_tokens>0) AS cache_samples,
 COUNT(*) FILTER (WHERE first_token_eligible AND first_token_ms>=0) AS first_token_samples,
 COUNT(*) FILTER (WHERE duration_ms>=0) AS duration_samples
FROM measurements GROUP BY account_id`

func (r *opsRepository) GetAccountRequestStats(ctx context.Context, accountIDs []int64, startTime, endTime time.Time) (map[string]*service.OpsAccountRequestStats, error) {
	if r == nil || r.db == nil {
		return nil, fmt.Errorf("nil ops repository")
	}
	stats := make(map[string]*service.OpsAccountRequestStats, len(accountIDs))
	if len(accountIDs) == 0 {
		return stats, nil
	}
	rows, err := r.db.QueryContext(ctx, opsAccountRequestStatsSQL, startTime.UTC(), endTime.UTC(), pq.Array(accountIDs))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var accountID int64
		entry := &service.OpsAccountRequestStats{WindowStart: startTime, WindowEnd: endTime}
		if err := rows.Scan(&accountID, &entry.CacheRate, &entry.AvgFirstTokenMs, &entry.AvgDurationMs,
			&entry.CacheSamples, &entry.FirstTokenSamples, &entry.DurationSamples); err != nil {
			return nil, err
		}
		stats[strconv.FormatInt(accountID, 10)] = entry
	}
	return stats, rows.Err()
}
