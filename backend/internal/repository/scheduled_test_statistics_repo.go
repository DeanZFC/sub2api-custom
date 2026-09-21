package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *scheduledTestResultRepository) ListStatisticsAccountIDs(ctx context.Context, groupID int64) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
JOIN account_groups ag ON ag.account_id=a.id
WHERE ag.group_id=$1 AND a.deleted_at IS NULL ORDER BY a.id`, groupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *scheduledTestResultRepository) CollectStatistics(ctx context.Context, filter service.ScheduledTestStatisticsFilter) (*service.ScheduledTestStatistics, error) {
	if filter.WindowStart.IsZero() || !filter.WindowEnd.After(filter.WindowStart) || strings.TrimSpace(filter.Model) == "" || (filter.GroupID == nil && filter.AccountID == nil) {
		return nil, fmt.Errorf("statistics require a bounded window, model and target")
	}
	args := []any{filter.WindowStart, filter.WindowEnd, strings.TrimSpace(filter.Model)}
	usageScope, errorScope := "", ""
	if filter.GroupID != nil {
		args = append(args, *filter.GroupID)
		usageScope += fmt.Sprintf(" AND ul.group_id=$%d", len(args))
		errorScope += fmt.Sprintf(" AND e.group_id=$%d", len(args))
	}
	if filter.AccountID != nil {
		args = append(args, *filter.AccountID)
		usageScope += fmt.Sprintf(" AND ul.account_id=$%d", len(args))
		errorScope += fmt.Sprintf(" AND e.account_id=$%d", len(args))
	}
	// Positive costs OR usage identify free/zero-multiplier successes, while
	// all-zero failed-request placeholders cannot establish a successful result.
	// A billed partial response may subsequently fail: final errors take precedence.
	// WS errors have connection-level IDs, so preserve each failed turn separately
	// instead of correlating that connection ID with unrelated successful turns.
	query := `WITH usage_candidates AS MATERIALIZED (
 SELECT DISTINCT ON (ul.api_key_id, COALESCE(NULLIF(ul.request_id,''), 'usage:'||ul.id::text))
   ul.id, ul.created_at, ul.api_key_id, ul.request_id, ul.request_type,
   ul.input_tokens, ul.image_input_tokens, ul.cache_creation_tokens, ul.cache_read_tokens,
   ul.first_token_ms, ul.native_compaction_v2, ul.inbound_endpoint, ul.upstream_endpoint
 FROM usage_logs ul
 WHERE ul.created_at >= $1 AND ul.created_at < $2
   AND COALESCE(NULLIF(TRIM(ul.requested_model),''), ul.model) = $3` + usageScope + `
   AND COALESCE(ul.request_type,0) NOT IN (4,6)
   AND (ul.actual_cost>0 OR ul.total_cost>0 OR ul.input_tokens>0 OR ul.output_tokens>0
        OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_output_tokens>0
        OR ul.image_input_tokens>0 OR ul.image_count>0 OR ul.video_count>0)
 ORDER BY ul.api_key_id, COALESCE(NULLIF(ul.request_id,''), 'usage:'||ul.id::text), ul.created_at DESC, ul.id DESC
), error_candidates AS (
 SELECT e.id, e.api_key_id, NULLIF(TRIM(e.request_id),'') AS request_id,
   NULLIF(TRIM(e.client_request_id),'') AS client_request_id, e.request_type, e.created_at,
   CASE WHEN e.request_type=3 THEN 'ws-error:'||e.id::text
     ELSE COALESCE('client:'||NULLIF(TRIM(e.client_request_id),''),
                   'local:'||NULLIF(TRIM(e.request_id),''), 'error:'||e.id::text) END AS outcome_key
 FROM ops_error_logs e
 WHERE e.created_at >= $1 AND e.created_at < $2
   AND COALESCE(NULLIF(TRIM(e.requested_model),''), e.model) = $3` + errorScope + `
   AND NOT COALESCE(e.is_count_tokens,false)
   AND (COALESCE(e.status_code,0)>=400 OR e.error_type='cyber_policy')
), failed AS MATERIALIZED (
 SELECT DISTINCT ON (api_key_id, outcome_key)
   id, created_at, api_key_id, request_id, client_request_id, request_type
 FROM error_candidates ORDER BY api_key_id, outcome_key, created_at DESC, id DESC
), failure_keys AS MATERIALIZED (
 SELECT DISTINCT COALESCE(e.api_key_id,0) AS api_key_id, keys.request_id
 FROM failed e CROSS JOIN LATERAL unnest(ARRAY[
   'client:'||e.client_request_id, 'local:'||e.request_id, e.request_id, e.client_request_id
 ]) AS keys(request_id)
 WHERE COALESCE(e.request_type,0)<>3 AND keys.request_id IS NOT NULL
), successful_usage AS (
 SELECT ul.* FROM usage_candidates ul
 WHERE COALESCE(ul.request_type,0)=3 OR NOT EXISTS (
   SELECT 1 FROM failure_keys e
   WHERE e.api_key_id=COALESCE(ul.api_key_id,0) AND e.request_id=ul.request_id
 )
), recent_requests AS (
 SELECT id, created_at, success FROM (
   SELECT id, created_at, true AS success FROM successful_usage
   UNION ALL
   SELECT id, created_at, false AS success FROM failed
 ) outcomes
 ORDER BY created_at DESC, id DESC, success DESC LIMIT 10
), success_metrics AS (
 SELECT COUNT(*) AS success_requests,
 COALESCE(SUM(GREATEST(cache_read_tokens,0)) FILTER (WHERE NOT COALESCE(native_compaction_v2,false)
   AND COALESCE(inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(upstream_endpoint,'') NOT LIKE '%/compact%'),0) AS cache_read,
 COALESCE(SUM(GREATEST(input_tokens-image_input_tokens,0)::bigint+GREATEST(cache_creation_tokens,0)+GREATEST(cache_read_tokens,0)) FILTER (WHERE NOT COALESCE(native_compaction_v2,false)
   AND COALESCE(inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(upstream_endpoint,'') NOT LIKE '%/compact%'),0) AS cache_input,
 (AVG(first_token_ms) FILTER (WHERE first_token_ms>=0))::float8 AS avg_first_token_ms,
 COUNT(first_token_ms) FILTER (WHERE first_token_ms>=0) AS first_token_samples
 FROM successful_usage
)
SELECT success_requests, (SELECT COUNT(*) FROM failed), cache_read,cache_input,avg_first_token_ms,first_token_samples,
 (SELECT COALESCE(jsonb_agg(jsonb_build_object('success',success,'created_at',created_at)
                          ORDER BY created_at DESC,id DESC,success DESC),'[]'::jsonb)
  FROM recent_requests)
FROM success_metrics`
	result := &service.ScheduledTestStatistics{WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd}
	var recentJSON []byte
	err := r.db.QueryRowContext(ctx, query, args...).Scan(&result.SuccessRequests, &result.FailedRequests, &result.CacheReadTokens, &result.CacheInputTokens, &result.AvgFirstTokenMs, &result.FirstTokenSamples, &recentJSON)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(recentJSON, &result.RecentRequests); err != nil {
		return nil, fmt.Errorf("decode recent statistics requests: %w", err)
	}
	result.TotalRequests = result.SuccessRequests + result.FailedRequests
	if result.TotalRequests > 0 {
		rate := float64(result.SuccessRequests) / float64(result.TotalRequests)
		result.SuccessRate = &rate
	}
	if result.CacheInputTokens > 0 {
		rate := float64(result.CacheReadTokens) / float64(result.CacheInputTokens)
		result.CacheRate = &rate
	}
	return result, nil
}
