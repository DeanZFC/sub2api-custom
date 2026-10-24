package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.GroupQualityTargetSummaryRepository = (*passiveQualityRepository)(nil)

func (r *passiveQualityRepository) GroupQualityTargetSummaries(ctx context.Context, now time.Time) ([]service.GroupQualityTargetSummary, error) {
	policies, err := r.ListPolicies(ctx)
	if err != nil {
		return nil, err
	}
	out := []service.GroupQualityTargetSummary{}
	for _, p := range policies {
		if !p.IsGroupQualityTarget() || len(p.GroupIDs) != 1 {
			continue
		}
		s, err := r.groupQualityTargetSummary(ctx, p, now)
		if err != nil {
			return nil, err
		}
		s.PolicyID = p.ID
		out = append(out, s)
	}
	return out, nil
}

func (r *passiveQualityRepository) groupQualityTargetSummary(ctx context.Context, p service.PassiveQualityPolicy, now time.Time) (service.GroupQualityTargetSummary, error) {
	groupID := p.GroupIDs[0]
	s := service.GroupQualityTargetSummary{GroupID: groupID, WindowSeconds: 3600, GeneratedAt: now}
	var cache, availability, ttft sql.NullFloat64
	var totalInput int64
	// Account/request identity is retained across WS turns. A failed attempt
	// wins over partial usage, matching the real-traffic protection collector.
	err := r.db.QueryRowContext(ctx, `WITH failed AS MATERIALIZED (
 SELECT account_id,request_key,created_at FROM passive_quality_failures
 WHERE group_id=$1 AND NOT non_text AND created_at>=$2 AND created_at<=$3
 AND ($4='' OR model=$4) AND NOT(account_id=ANY($5))
 ), succeeded AS MATERIALIZED (
 SELECT DISTINCT ON (ul.account_id,COALESCE(NULLIF(COALESCE(NULLIF(cr.attempt_key,''),ul.request_id),''),'usage:'||ul.id))
 ul.account_id,COALESCE(NULLIF(COALESCE(NULLIF(cr.attempt_key,''),ul.request_id),''),'usage:'||ul.id) AS request_key,
 CASE WHEN (ul.stream OR ul.openai_ws_mode) AND ul.first_token_ms>=0 THEN ul.first_token_ms END AS ttft,
 CASE WHEN NOT COALESCE(ul.native_compaction_v2,false)
 AND COALESCE(ul.inbound_endpoint,'') NOT LIKE '%/compact%'
 AND COALESCE(ul.upstream_endpoint,'') NOT LIKE '%/compact%'
 THEN GREATEST(ul.input_tokens-ul.image_input_tokens,0)::bigint+GREATEST(ul.cache_creation_tokens,0)+GREATEST(ul.cache_read_tokens,0) ELSE 0 END AS text_input,
 GREATEST(ul.cache_read_tokens,0)::bigint AS cache_read,
 (ul.cache_read_tokens>0 OR COALESCE(cr.cache_reported,false)) AS cache_reported
 FROM usage_logs ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE ul.group_id=$1 AND ul.created_at>=$2 AND ul.created_at<=$3 AND ul.duration_ms IS NOT NULL
 AND ($4='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$4) AND NOT(ul.account_id=ANY($5))
 AND COALESCE(ul.request_type,0) NOT IN(4,6)
 AND `+passiveGroupTargetTextUsageSQL+`
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_input_tokens>0 OR ul.image_output_tokens>0 OR ul.total_cost>0 OR ul.actual_cost>0 OR ul.image_count>0 OR ul.video_count>0)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=ul.account_id AND NOT f.non_text AND f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id))
 ORDER BY ul.account_id,COALESCE(NULLIF(COALESCE(NULLIF(cr.attempt_key,''),ul.request_id),''),'usage:'||ul.id),ul.created_at DESC,ul.id DESC
 ), stats AS (
 SELECT count(*) AS successes,count(*) FILTER(WHERE ttft IS NOT NULL) AS ttft_samples,
 percentile_disc(.9) WITHIN GROUP(ORDER BY ttft) FILTER(WHERE ttft IS NOT NULL) AS ttft_p90,
 count(*) FILTER(WHERE text_input>0 AND cache_reported) AS cache_samples,
 COALESCE(sum(cache_read) FILTER(WHERE text_input>0 AND cache_reported),0) AS cache_read,
 COALESCE(sum(text_input) FILTER(WHERE text_input>0 AND cache_reported),0) AS cache_input,
 count(*) FILTER(WHERE text_input>0 AND NOT cache_reported) AS cache_unknown FROM succeeded
 ), failures AS (SELECT count(*) AS failures FROM failed), unknown AS (
 SELECT count(*) AS failures FROM passive_quality_failures f
 WHERE f.group_id=0 AND NOT f.non_text AND f.created_at>=$2 AND f.created_at<=$3
 AND ($4='' OR f.model=$4) AND NOT(f.account_id=ANY($5))
 AND (EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=f.account_id AND ag.group_id=$1)
 OR EXISTS(SELECT 1 FROM succeeded s WHERE s.account_id=f.account_id))
 ), telemetry AS (
 SELECT NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gaps WHERE created_at>=$2 AND created_at<=$3) AS complete
 ) SELECT stats.cache_samples,stats.cache_read,stats.cache_input,stats.cache_unknown,
 CASE WHEN telemetry.complete THEN 100.0*stats.cache_read/NULLIF(stats.cache_input,0) END,
 stats.successes,failures.failures,unknown.failures,
 CASE WHEN telemetry.complete AND unknown.failures=0 THEN 100.0*stats.successes/NULLIF(stats.successes+failures.failures,0) END,
 stats.ttft_samples,CASE WHEN telemetry.complete THEN stats.ttft_p90 END,telemetry.complete,
 (SELECT COALESCE(sum(text_input),0) FROM succeeded)
 FROM stats CROSS JOIN failures CROSS JOIN unknown CROSS JOIN telemetry`, groupID, now.Add(-time.Hour), now, p.Model, pq.Array(append([]int64{}, p.ExcludedAccountIDs...))).Scan(
		&s.CacheSamples, &s.CacheReadTokens, &s.CacheInputTokens, &s.CacheUnknownSamples, &cache,
		&s.SuccessSamples, &s.FailureSamples, &s.FailureUnknownSamples, &availability, &s.TTFTSamples, &ttft, &s.TelemetryComplete, &totalInput)
	if err != nil {
		return s, err
	}
	if cache.Valid {
		s.ActualCacheRate = &cache.Float64
	}
	if availability.Valid {
		s.ActualAvailability = &availability.Float64
	}
	if ttft.Valid {
		s.TTFTP90MS = &ttft.Float64
	}
	if err = r.updateGroupQualityTrafficSnapshot(ctx, p, now, totalInput, s.CacheReadTokens, s.TelemetryComplete); err != nil {
		return s, err
	}
	s.TrafficControl, err = r.groupQualityTrafficSummary(ctx, p, now, totalInput, s.CacheReadTokens, s.TelemetryComplete)
	if err == nil {
		s.Fallback, err = r.groupQualityFallbackSummary(ctx, p, now)
	}
	return s, err
}
