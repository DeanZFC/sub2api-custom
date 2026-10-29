package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Aggregate in PostgreSQL: traffic volume never becomes a sample ceiling or an
// unbounded Go allocation. The report keeps the complete hour after recovery;
// a separate post-recovery population gates a new protection episode.
func passiveActiveGroupTargetSamples(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) (service.PassiveQualityEvidence, error) {
	from := now.Add(-time.Hour)
	if s.ObserveAfter.After(from) {
		from = s.ObserveAfter
	}
	// Independent cursors stop dense success traffic consuming another metric's batch.
	cursor := func(metric string) time.Time {
		at := s.LastEvidenceAt
		if clock, ok := s.ActiveConfirmations[metric]; ok {
			at = clock.Latest
		}
		if s.RecoveredAt.After(at) {
			at = s.RecoveredAt
		}
		return at
	}
	rows, err := tx.QueryContext(ctx, `WITH failed AS MATERIALIZED (
 SELECT request_key AS key,created_at,started_at,true AS failed,NULL::float8 AS ttft,0::bigint AS cache_input,0::bigint AS cache_read,0::bigint AS output,0::float8 AS generation_ms
 FROM passive_quality_failures
 WHERE account_id=$1 AND group_id=$9 AND NOT non_text AND ($2='' OR model=$2)
 AND created_at>=$3 AND created_at<=$4 AND started_at>=$5
 ), succeeded AS (
 SELECT DISTINCT ON (COALESCE(NULLIF(cr.attempt_key,''),NULLIF(ul.request_id,''),'usage:'||ul.id))
 COALESCE(NULLIF(cr.attempt_key,''),NULLIF(ul.request_id,''),'usage:'||ul.id) AS key,ul.created_at,
 ul.created_at-GREATEST(ul.duration_ms,0)*INTERVAL '1 millisecond' AS started_at,false AS failed,
 CASE WHEN (ul.stream OR ul.openai_ws_mode) AND ul.first_token_ms>=0 THEN ul.first_token_ms::float8 END AS ttft,
 CASE WHEN (ul.cache_read_tokens>0 OR cr.cache_reported) AND NOT COALESCE(ul.native_compaction_v2,false)
 AND COALESCE(ul.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(ul.upstream_endpoint,'') NOT LIKE '%/compact%'
 AND ul.input_tokens>=0 AND ul.image_input_tokens>=0 AND ul.cache_creation_tokens>=0 AND ul.cache_read_tokens>=0
 THEN GREATEST(ul.input_tokens-ul.image_input_tokens,0)::bigint+ul.cache_creation_tokens+ul.cache_read_tokens ELSE 0 END AS cache_input,
 GREATEST(ul.cache_read_tokens,0)::bigint AS cache_read,
 CASE WHEN `+outputTPSEligibleSQL("ul")+` THEN ul.output_tokens ELSE 0 END AS output,
 CASE WHEN `+outputTPSEligibleSQL("ul")+` THEN ul.duration_ms::float8-ul.first_token_ms::float8 ELSE 0 END AS generation_ms
 FROM usage_logs ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE ul.account_id=$1 AND ul.group_id=$9 AND ($2='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$2)
 AND ul.created_at>=$3 AND ul.created_at<=$4 AND ul.duration_ms IS NOT NULL
 AND ul.created_at-GREATEST(ul.duration_ms,0)*INTERVAL '1 millisecond'>=$5
 AND COALESCE(ul.request_type,0) NOT IN (4,6) AND `+passiveGroupTargetTextUsageSQL+`
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_input_tokens>0 OR ul.total_cost>0 OR ul.actual_cost>0)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=$1 AND NOT f.non_text AND f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id))
 ORDER BY COALESCE(NULLIF(cr.attempt_key,''),NULLIF(ul.request_id,''),'usage:'||ul.id),ul.created_at DESC,ul.id DESC
 ), outcomes AS (SELECT * FROM failed UNION ALL SELECT * FROM succeeded), scopes AS (
 SELECT o.*,v.scope,row_number() OVER (PARTITION BY v.scope ORDER BY created_at DESC,key DESC) AS seq
 FROM outcomes o CROSS JOIN LATERAL (VALUES (0,true),(1,o.created_at>$6),(2,o.created_at>$7 AND o.started_at>=$7),(3,o.created_at>GREATEST($6,$7) AND o.started_at>=$7),
 (4,o.created_at>$10 AND o.started_at>=$7),(5,o.created_at>$11 AND o.started_at>=$7),
 (6,o.created_at>$12 AND o.started_at>=$7),(7,o.created_at>$13 AND o.started_at>=$7)) v(scope,include) WHERE v.include
 ), ranked AS (SELECT *,min(seq) FILTER (WHERE NOT failed) OVER (PARTITION BY scope) AS first_success FROM scopes)
 SELECT scope,jsonb_build_object(
 'samples',count(*),'failures',count(*) FILTER (WHERE failed),
 'consecutive_failures',count(*) FILTER (WHERE failed AND (first_success IS NULL OR seq<first_success)),
 'recent_consecutive_failures',count(*) FILTER (WHERE failed AND (first_success IS NULL OR seq<first_success) AND created_at>=$8),
 'error_rate',100.0*count(*) FILTER (WHERE failed)/NULLIF(count(*),0),
 'ttft',percentile_disc(0.9) WITHIN GROUP (ORDER BY ttft) FILTER (WHERE NOT failed AND ttft IS NOT NULL),
 'ttft_samples',count(*) FILTER (WHERE NOT failed AND ttft IS NOT NULL),
 'cache_samples',count(*) FILTER (WHERE NOT failed AND cache_input>0),
 'cache_input_tokens',COALESCE(sum(cache_input) FILTER (WHERE NOT failed AND cache_input>0),0),
 'cache_read_tokens',COALESCE(sum(cache_read) FILTER (WHERE NOT failed AND cache_input>0),0),
 'cache_rate',100.0*sum(cache_read) FILTER (WHERE NOT failed AND cache_input>0)/NULLIF(sum(cache_input) FILTER (WHERE NOT failed AND cache_input>0),0),
 'tps_samples',count(*) FILTER (WHERE NOT failed AND output>0 AND generation_ms>0),
 'output_tps',1000.0*sum(output) FILTER (WHERE NOT failed AND output>0 AND generation_ms>0)/NULLIF(sum(generation_ms) FILTER (WHERE NOT failed AND output>0 AND generation_ms>0),0),
 'latest',max(created_at),'ttft_latest',max(created_at) FILTER (WHERE NOT failed AND ttft IS NOT NULL),
 'cache_latest',max(created_at) FILTER (WHERE NOT failed AND cache_input>0),
 'tps_latest',max(created_at) FILTER (WHERE NOT failed AND output>0 AND generation_ms>0))
 FROM ranked GROUP BY scope ORDER BY scope`, s.AccountID, p.Model, from, now, s.ObserveAfter, s.LastEvidenceAt, s.RecoveredAt, now.Add(-time.Minute), p.GroupIDs[0], cursor("error_rate"), cursor("cache_rate"), cursor("ttft_p90"), cursor("output_tps"))
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	var evidence [8]service.PassiveQualityEvidence
	for rows.Next() {
		var scope int
		var raw []byte
		if err = rows.Scan(&scope, &raw); err != nil {
			_ = rows.Close()
			return service.PassiveQualityEvidence{}, err
		}
		if err = json.Unmarshal(raw, &evidence[scope]); err != nil {
			_ = rows.Close()
			return service.PassiveQualityEvidence{}, err
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	complete, err := passiveTelemetryComplete(ctx, tx, from, now, s.AccountID, p.Model, p.GroupIDs[0], "all")
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	if complete && p.ErrorRate.Enabled {
		err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM passive_quality_failures WHERE account_id=$1 AND group_id=0 AND NOT non_text AND ($2='' OR model=$2) AND created_at>=$3 AND created_at<=$4 AND started_at>=$5)`, s.AccountID, p.Model, from, now, s.ObserveAfter).Scan(&complete)
	}
	for _, i := range []int{0, 2} {
		e := &evidence[i]
		e.Complete, e.ProbeModel = complete, p.Model
		e.Fresh, e.CacheFresh, e.TTFTFresh, e.TPSFresh = evidence[4].Samples, evidence[5].CacheSamples, evidence[6].TTFTSamples, evidence[7].TPSSamples
		e.FreshErrorRate, e.FreshCacheRate, e.FreshTTFT, e.FreshOutputTPS = evidence[4].ErrorRate, evidence[5].CacheRate, evidence[6].TTFT, evidence[7].OutputTPS
	}
	if !s.RecoveredAt.IsZero() {
		evidence[0].SinceRecovery = &evidence[2]
	}
	return evidence[0], err
}
