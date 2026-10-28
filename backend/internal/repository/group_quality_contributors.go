package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func groupQualityContributors(ctx context.Context, reader groupQualitySummaryReader, p service.PassiveQualityPolicy, now time.Time) (out []service.GroupQualityContributor, total int, retErr error) {
	rows, err := reader.QueryContext(ctx, groupQualitySummaryPopulationSQL+`, outcomes AS (
 SELECT account_id,model,request_key,created_at,false AS failed,ttft,text_input,cache_read,cache_reported,output_tokens,generation_ms,tps_eligible FROM succeeded
 UNION ALL SELECT account_id,model,request_key,created_at,true,NULL::bigint,0,0,false,0,NULL::float8,false FROM failed
 ), attributed AS (
 SELECT o.*,COALESCE(l.kind,'unattributed') AS kind FROM outcomes o
 LEFT JOIN LATERAL (SELECT CASE WHEN fallback THEN 'fallback' WHEN validation THEN 'validation' ELSE 'normal' END AS kind
 FROM group_quality_traffic_leases l WHERE l.policy_id=$7 AND l.group_id=$1 AND l.account_id=o.account_id
 AND l.model=o.model AND l.request_key=o.request_key AND NOT l.unused AND l.created_at<=o.created_at
 ORDER BY l.created_at DESC LIMIT 1) l ON true
 ), grouped AS (
 SELECT account_id,model,kind,count(*) FILTER(WHERE NOT failed) AS successes,count(*) FILTER(WHERE failed) AS failures,
 count(*) FILTER(WHERE NOT failed AND text_input>0 AND cache_reported) AS cache_samples,
 count(*) FILTER(WHERE NOT failed AND text_input>0 AND NOT cache_reported) AS unknown_samples,
 COALESCE(sum(text_input) FILTER(WHERE NOT failed AND text_input>0 AND cache_reported),0)::bigint AS cache_input,
 COALESCE(sum(cache_read) FILTER(WHERE NOT failed AND text_input>0 AND cache_reported),0)::bigint AS cache_read,
 count(*) FILTER(WHERE NOT failed AND ttft IS NOT NULL) AS ttft_samples,
 count(*) FILTER(WHERE NOT failed AND ttft IS NOT NULL AND $6>0 AND ttft>=$6) AS slow_requests,
 percentile_disc(.9) WITHIN GROUP(ORDER BY ttft) FILTER(WHERE NOT failed AND ttft IS NOT NULL) AS ttft_p90,
 count(*) FILTER(WHERE tps_eligible) AS tps_samples,
 COALESCE(sum(output_tokens) FILTER(WHERE tps_eligible),0)::bigint AS tps_output_tokens,
 COALESCE(sum(generation_ms) FILTER(WHERE tps_eligible),0)::float8 AS tps_generation_ms
 FROM attributed GROUP BY account_id,model,kind
 ) SELECT g.account_id,COALESCE(a.name,''),g.model,g.kind,g.successes,g.failures,g.cache_samples,g.unknown_samples,g.cache_input,g.cache_read,
 GREATEST(g.cache_input-g.cache_read,0),g.ttft_samples,g.slow_requests,g.ttft_p90,g.tps_samples,g.tps_output_tokens,g.tps_generation_ms,count(*) OVER()
 FROM grouped g LEFT JOIN accounts a ON a.id=g.account_id
 ORDER BY GREATEST(g.cache_input-g.cache_read,0) DESC,g.failures DESC,g.slow_requests DESC,g.account_id,g.model,g.kind LIMIT 200`,
		p.GroupIDs[0], now.Add(-time.Hour), now, p.Model, pq.Array(append([]int64{}, p.ExcludedAccountIDs...)), func() float64 {
			if p.TTFT.Enabled {
				return p.TTFT.Pause
			}
			return 0
		}(), p.ID)
	if err != nil {
		return nil, 0, err
	}
	defer func() {
		if closeErr := rows.Close(); retErr == nil && closeErr != nil {
			retErr = closeErr
		}
	}()
	out = []service.GroupQualityContributor{}
	total = 0
	for rows.Next() {
		var c service.GroupQualityContributor
		var ttft sql.NullFloat64
		var tpsOutputTokens int64
		var tpsGenerationMS float64
		if err = rows.Scan(&c.AccountID, &c.AccountName, &c.Model, &c.TrafficKind, &c.SuccessSamples, &c.FailureSamples, &c.CacheSamples, &c.CacheUnknownSamples, &c.CacheInputTokens, &c.CacheReadTokens, &c.UncachedTokens, &c.TTFTSamples, &c.SlowRequests, &ttft,
			&c.TPSSamples, &tpsOutputTokens, &tpsGenerationMS, &total); err != nil {
			return nil, 0, err
		}
		if ttft.Valid {
			c.TTFTP90MS = &ttft.Float64
		}
		c.OutputTPS = service.OutputTokensPerSecond(tpsOutputTokens, tpsGenerationMS)
		out = append(out, c)
	}
	return out, total, rows.Err()
}
