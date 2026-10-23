package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Keep live targets and their summary on the same text-output population.
// Image inputs remain valid text requests; generated media does not.
const passiveGroupTargetTextUsageSQL = `COALESCE(ul.image_count,0)=0
 AND COALESCE(ul.video_count,0)=0 AND COALESCE(ul.image_output_tokens,0)=0
 AND COALESCE(ul.inbound_endpoint,'') !~* '(^|/)(images|videos|audio)(/|$)'
 AND COALESCE(ul.upstream_endpoint,'') !~* '(^|/)(images|videos|audio)(/|$)'`

// Measured group targets do not infer provider cache reuse opportunities. Only
// explicit reported counters (including zero) or positive reads are evidence.
// Non-streaming requests may report real counters too; TTFT still needs a stream.
func passiveGroupTargetSamples(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s service.PassiveQualityState, now time.Time) (service.PassiveQualityEvidence, error) {
	from := now.Add(-time.Duration(p.WindowSeconds) * time.Second)
	if s.ObserveAfter.After(from) {
		from = s.ObserveAfter
	}
	started := s.ObserveAfter
	if s.Phase == "trial" {
		from, started = s.TrialStartedAt, s.TrialStartedAt
	}
	rows, err := tx.QueryContext(ctx, `WITH failed AS MATERIALIZED (
 SELECT request_key,created_at,model FROM passive_quality_failures
 WHERE account_id=$1 AND group_id=$7 AND NOT non_text AND ($2='' OR model=$2)
 AND created_at>=$3 AND created_at<=$4 AND started_at>=$5
 ORDER BY created_at DESC,id DESC LIMIT $6
 ), succeeded AS (
 SELECT COALESCE(NULLIF(cr.attempt_key,''),ul.request_id) AS request_id,ul.id,ul.created_at,
 CASE WHEN ul.stream OR ul.openai_ws_mode THEN ul.first_token_ms END AS first_token_ms,
 CASE WHEN (ul.cache_read_tokens>0 OR cr.cache_reported) AND NOT COALESCE(ul.native_compaction_v2,false)
 AND COALESCE(ul.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(ul.upstream_endpoint,'') NOT LIKE '%/compact%'
 THEN GREATEST(ul.input_tokens-ul.image_input_tokens,0)::bigint+GREATEST(ul.cache_creation_tokens,0)+GREATEST(ul.cache_read_tokens,0) ELSE 0 END AS cache_input,
 GREATEST(ul.cache_read_tokens,0)::bigint AS cache_read,COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model) AS model
 FROM usage_logs ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE ul.account_id=$1 AND ul.group_id=$7 AND ($2='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$2)
 AND ul.created_at>=$3 AND ul.created_at<=$4 AND ul.duration_ms IS NOT NULL
 AND ul.created_at-GREATEST(ul.duration_ms,0)*INTERVAL '1 millisecond'>=$5
 AND COALESCE(ul.request_type,0) NOT IN (4,6)
 AND `+passiveGroupTargetTextUsageSQL+`
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_input_tokens>0 OR ul.image_output_tokens>0 OR ul.total_cost>0 OR ul.actual_cost>0 OR ul.image_count>0 OR ul.video_count>0)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=$1 AND NOT f.non_text AND f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id))
 ORDER BY ul.created_at DESC,ul.id DESC LIMIT $6
 ), outcomes AS (
 SELECT DISTINCT ON (COALESCE(NULLIF(request_id,''),'usage:'||id)) COALESCE(NULLIF(request_id,''),'usage:'||id) AS key,created_at,false AS failed,first_token_ms::float8,cache_input,cache_read,model
 FROM succeeded ORDER BY COALESCE(NULLIF(request_id,''),'usage:'||id),created_at DESC
 ) SELECT key,created_at,failed,first_token_ms,cache_input,cache_read,COALESCE(model,'') FROM (
 SELECT * FROM outcomes UNION ALL SELECT request_key,created_at,true,NULL::float8,0,0,model FROM failed
 ) combined ORDER BY created_at DESC,key DESC LIMIT $6`, s.AccountID, p.Model, from, now, started, p.MaxSamples, p.GroupIDs[0])
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	samples := []service.PassiveQualitySample{}
	for rows.Next() {
		var v service.PassiveQualitySample
		if err = rows.Scan(&v.Key, &v.At, &v.Failed, &v.TTFT, &v.CacheInput, &v.CacheRead, &v.Model); err != nil {
			_ = rows.Close()
			return service.PassiveQualityEvidence{}, err
		}
		samples = append(samples, v)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return service.PassiveQualityEvidence{}, err
	}
	var complete bool
	err = tx.QueryRowContext(ctx, `SELECT NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gaps WHERE created_at>=$1 AND created_at<=$2)
 AND (NOT $6 OR NOT EXISTS(SELECT 1 FROM passive_quality_failures WHERE account_id=$3 AND group_id=0 AND NOT non_text AND ($4='' OR model=$4) AND created_at>=$1 AND created_at<=$2 AND started_at>=$5))`, from, now, s.AccountID, p.Model, started, p.ErrorRate.Enabled).Scan(&complete)
	return service.PassiveQualitySummarize(samples, s.LastEvidenceAt, complete), err
}

func beginPassiveTrial(p service.PassiveQualityPolicy, s *service.PassiveQualityState, now time.Time, reason string) {
	s.Phase, s.Reason = "trial", reason
	s.Generation++
	s.TrialStartedAt = now
	s.TrialEndsAt = now.Add(time.Duration(p.TrialSeconds) * time.Second)
	s.Used, s.Inflight, s.Hits = 0, 0, 0
	s.LastEvidenceAt = now
	s.Evidence = service.PassiveQualityEvidence{Complete: true}
}
