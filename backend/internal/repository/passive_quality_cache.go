package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Reuse eligibility is reconstructed exclusively from request-time digests and
// earlier completed requests. Neither current nor previous cache-hit results
// participate in deciding whether reuse was possible. Missing history is unknown.
func passiveCacheSamples(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) ([]service.PassiveQualitySample, bool, error) {
	c := p.CacheProtection
	if c == nil {
		return nil, false, nil
	}
	from := now.Add(-time.Duration(max(c.Quick.WindowSeconds, c.ActionWindow.WindowSeconds)) * time.Second)
	if p.UpdatedAt.After(from) {
		from = p.UpdatedAt
	}
	// Exemptions reset all independent evidence; availability trials do not.
	if s.CacheObserveAfter.After(from) {
		from = s.CacheObserveAfter
	}
	limit := max(c.Quick.MaxSamples, c.ActionWindow.MaxSamples)
	rows, err := tx.QueryContext(ctx, `WITH recent_usage AS MATERIALIZED (
 SELECT * FROM usage_logs WHERE account_id=$1 AND created_at>=$3 AND created_at<=$4
 AND ($2='' OR COALESCE(NULLIF(TRIM(requested_model),''),model)=$2)
 ORDER BY created_at DESC,id DESC LIMIT 10001
 ), candidates AS MATERIALIZED (
 SELECT ul.*,cr.cache_metadata,cr.cache_reported,
 COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model) AS quality_model,
 row_number() OVER (PARTITION BY COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model) ORDER BY ul.created_at DESC,ul.id DESC) AS n
 FROM recent_usage ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE ul.account_id=$1 AND ($2='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$2)
 AND ul.created_at>=$3 AND ul.created_at<=$4 AND ul.duration_ms IS NOT NULL
 AND ul.created_at-GREATEST(ul.duration_ms,0)*INTERVAL '1 millisecond'>=$3
 AND COALESCE(ul.request_type,0) NOT IN (4,6)
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=ul.account_id AND f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id))
 ), selected AS (SELECT * FROM candidates WHERE n<=$5 ORDER BY created_at DESC,id DESC LIMIT 10001)
 SELECT COALESCE(NULLIF(ul.request_id,''),'usage:'||ul.id),ul.created_at,COALESCE(ul.quality_model,''),
 CASE WHEN `+channelMonitorV2CacheEligibleUL+` AND (ul.cache_read_tokens>0 OR ul.cache_reported) AND NOT COALESCE(ul.native_compaction_v2,false)
 AND COALESCE(ul.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(ul.upstream_endpoint,'') NOT LIKE '%/compact%'
 THEN GREATEST(ul.input_tokens-ul.image_input_tokens,0)::bigint+GREATEST(ul.cache_creation_tokens,0)+GREATEST(ul.cache_read_tokens,0) ELSE 0 END,
 GREATEST(ul.cache_read_tokens,0)::bigint,COALESCE(ul.cache_reported,false) OR ul.cache_read_tokens>0,
 COALESCE(ul.cache_metadata->>'session_hash',''),COALESCE(ul.cache_metadata->>'prompt_hash',''),
 CASE WHEN COALESCE(ul.cache_metadata->>'version','')<>'1' OR COALESCE(ul.cache_metadata->>'session_hash','')='' THEN 'unknown'
 WHEN COALESCE(ul.cache_metadata->>'unsupported_reason','')<>'' THEN 'unknown'
 WHEN COALESCE(ul.cache_metadata->>'wire_verified','false')<>'true' OR COALESCE(ul.cache_metadata->>'scope','')='' OR COALESCE(ul.cache_metadata->>'model','')='' THEN 'unknown'
 WHEN (ul.cache_metadata->>'started_at')::timestamptz>ul.created_at THEN 'unknown'
 WHEN recent.account_id IS NOT NULL AND recent.account_id<>$1 THEN 'cold'
 WHEN prior.samples >= $7 AND prior.tokens>=COALESCE((ul.cache_metadata->>'minimum_tokens')::bigint,1024) THEN 'reusable'
 WHEN prior.samples>0 THEN 'unknown' ELSE 'cold' END,
 CASE WHEN COALESCE(ul.cache_metadata->>'version','')<>'1' THEN 'reuse_history_unavailable'
 WHEN COALESCE(ul.cache_metadata->>'unsupported_reason','')<>'' THEN ul.cache_metadata->>'unsupported_reason'
 WHEN COALESCE(ul.cache_metadata->>'wire_verified','false')<>'true' THEN 'unverified_wire_request'
 WHEN COALESCE(ul.cache_metadata->>'session_hash','')='' THEN 'session_unknown'
 WHEN (ul.cache_metadata->>'started_at')::timestamptz>ul.created_at THEN 'invalid_request_chronology'
 WHEN recent.account_id IS NOT NULL AND recent.account_id<>$1 THEN 'account_switched'
 WHEN prior.samples >= $7 AND prior.tokens>=COALESCE((ul.cache_metadata->>'minimum_tokens')::bigint,1024) THEN 'matching_completed_prefix'
 WHEN prior.samples>0 THEN 'warmup_or_short_prefix'
 WHEN recent.started_at < (ul.cache_metadata->>'started_at')::timestamptz-LEAST($6,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second' THEN 'reuse_expired'
 ELSE 'first_placement_or_changed_prefix' END,COALESCE(prior.tokens,0)::bigint,
 (SELECT count(*)<=10000 FROM recent_usage),CASE WHEN ul.stream OR ul.openai_ws_mode THEN ul.first_token_ms END,
 CASE WHEN ul.input_cost>=0 AND ul.cache_creation_cost>=0 AND ul.cache_read_cost>=0
 THEN ul.input_cost+ul.cache_creation_cost+ul.cache_read_cost ELSE NULL END
 FROM selected ul
 LEFT JOIN LATERAL (
 SELECT count(*) AS samples,max(GREATEST(prev.input_tokens-prev.image_input_tokens,0)::bigint+GREATEST(prev.cache_creation_tokens,0)+GREATEST(prev.cache_read_tokens,0)) AS tokens
 FROM passive_quality_cache_reports pc JOIN usage_logs prev ON prev.account_id=pc.account_id AND prev.request_id=pc.request_key
 WHERE pc.account_id=$1 AND pc.cache_metadata->>'session_hash'=ul.cache_metadata->>'session_hash'
 AND pc.cache_metadata->>'scope'=ul.cache_metadata->>'scope'
 AND pc.cache_metadata->>'model'=ul.cache_metadata->>'model'
 AND COALESCE(pc.cache_metadata->>'unsupported_reason','')=''
 AND pc.cache_metadata->>'wire_verified'='true' AND pc.cache_metadata->>'version'='1'
 AND COALESCE(pc.cache_metadata->>'scope','')<>'' AND COALESCE(pc.cache_metadata->>'model','')<>''
 AND ul.cache_metadata->'prefix_hashes' ? (pc.cache_metadata->>'prompt_hash')
 AND (pc.cache_metadata->>'started_at')::timestamptz <= prev.created_at
 AND (pc.cache_metadata->>'started_at')::timestamptz >= (ul.cache_metadata->>'started_at')::timestamptz-LEAST($6,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300),COALESCE((pc.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second'
 AND pc.created_at < (ul.cache_metadata->>'started_at')::timestamptz
 AND pc.created_at >= (ul.cache_metadata->>'started_at')::timestamptz-LEAST($6,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300),COALESCE((pc.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second'
 AND prev.created_at < (ul.cache_metadata->>'started_at')::timestamptz
 AND prev.created_at >= (ul.cache_metadata->>'started_at')::timestamptz-LEAST($6,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second'
 AND prev.duration_ms IS NOT NULL AND COALESCE(prev.request_type,0) NOT IN (4,6)
 AND NOT COALESCE(prev.native_compaction_v2,false)
 AND COALESCE(prev.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(prev.upstream_endpoint,'') NOT LIKE '%/compact%'
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=pc.account_id AND f.request_key=COALESCE(NULLIF(pc.attempt_key,''),pc.request_key))
 ) prior ON true
 LEFT JOIN LATERAL (SELECT pc.account_id,pc.created_at,(pc.cache_metadata->>'started_at')::timestamptz AS started_at FROM passive_quality_cache_reports pc
 JOIN usage_logs prev ON prev.account_id=pc.account_id AND prev.request_id=pc.request_key
 WHERE pc.cache_metadata->>'session_hash'=ul.cache_metadata->>'session_hash'
 AND pc.created_at < (ul.cache_metadata->>'started_at')::timestamptz
 AND pc.created_at >= (ul.cache_metadata->>'started_at')::timestamptz-INTERVAL '2 days'
 AND prev.created_at < (ul.cache_metadata->>'started_at')::timestamptz
 ORDER BY pc.created_at DESC LIMIT 1) recent ON true
 ORDER BY ul.created_at DESC,ul.id DESC`, s.AccountID, p.Model, from, now, limit, c.ReuseWindowSeconds, c.WarmupRequests)
	if err != nil {
		return nil, false, err
	}
	samples := []service.PassiveQualitySample{}
	withinBudget := true
	for rows.Next() {
		var v service.PassiveQualitySample
		if err = rows.Scan(&v.Key, &v.At, &v.Model, &v.CacheInput, &v.CacheRead, &v.CacheReported, &v.CacheSession, &v.CachePrefix, &v.CacheOpportunity, &v.CacheReason, &v.CacheExpectedTokens, &withinBudget, &v.TTFT, &v.InputCost); err != nil {
			_ = rows.Close()
			return nil, false, err
		}
		samples = append(samples, v)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, false, err
	}
	var complete bool
	err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gaps WHERE created_at>=$1 AND created_at<=$2)`, from, now).Scan(&complete)
	complete = complete && withinBudget
	if len(samples) > 10000 {
		complete = false
		samples = samples[:10000]
	}
	return samples, complete, err
}
