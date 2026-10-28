package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func (r *channelQualityRepository) GetQualityScoreConfig(ctx context.Context) (service.ChannelQualityScoreConfig, error) {
	return getChannelQualityScoreConfig(ctx, r.db)
}
func (r *passiveQualityRepository) GetQualityScoreConfig(ctx context.Context) (service.ChannelQualityScoreConfig, error) {
	return getChannelQualityScoreConfig(ctx, r.db)
}
func (r *channelQualityRepository) SaveQualityScoreConfig(ctx context.Context, c *service.ChannelQualityScoreConfig) error {
	return saveChannelQualityScoreConfig(ctx, r.db, c)
}
func (r *passiveQualityRepository) SaveQualityScoreConfig(ctx context.Context, c *service.ChannelQualityScoreConfig) error {
	return saveChannelQualityScoreConfig(ctx, r.db, c)
}
func getChannelQualityScoreConfig(ctx context.Context, db *sql.DB) (service.ChannelQualityScoreConfig, error) {
	var c service.ChannelQualityScoreConfig
	var raw []byte
	var version int64
	var updated time.Time
	if err := db.QueryRowContext(ctx, `SELECT config,version,updated_at,collect_since FROM channel_quality_score_config WHERE id=1`).Scan(&raw, &version, &updated, &c.EvidenceSince); err != nil {
		return c, err
	}
	if err := json.Unmarshal(raw, &c); err != nil {
		return c, err
	}
	c.Version, c.UpdatedAt = version, updated
	return c, c.Validate()
}
func saveChannelQualityScoreConfig(ctx context.Context, db *sql.DB, c *service.ChannelQualityScoreConfig) error {
	if err := c.Validate(); err != nil {
		return err
	}
	raw, err := json.Marshal(c)
	if err != nil {
		return err
	}
	err = db.QueryRowContext(ctx, `UPDATE channel_quality_score_config SET config=$1,version=version+1,updated_at=clock_timestamp() WHERE id=1 AND version=$2 RETURNING version,updated_at,collect_since`, raw, c.Version).Scan(&c.Version, &c.UpdatedAt, &c.EvidenceSince)
	if err == sql.ErrNoRows {
		return service.ErrChannelQualityScoreConflict
	}
	if err == nil {
		invalidateChannelQualityScoreCache(db)
	}
	return err
}

func (r *channelQualityRepository) ChannelQualityScores(ctx context.Context, accounts []int64, model string) (map[int64]*service.ChannelQualityScore, error) {
	return queryChannelQualityScores(ctx, r.db, accounts, model)
}
func (r *passiveQualityRepository) ChannelQualityScores(ctx context.Context, accounts []int64, model string) (map[int64]*service.ChannelQualityScore, error) {
	return queryChannelQualityScores(ctx, r.db, accounts, model)
}

type channelQualityScoreCacheKey struct {
	db        *sql.DB
	accountID int64
	model     string
	version   int64
}
type channelQualityScoreCacheEntry struct {
	score   *service.ChannelQualityScore
	expires time.Time
}

var channelQualityScoreCache = struct {
	sync.Mutex
	entries map[channelQualityScoreCacheKey]channelQualityScoreCacheEntry
}{entries: make(map[channelQualityScoreCacheKey]channelQualityScoreCacheEntry)}

func invalidateChannelQualityScoreCache(db *sql.DB) {
	channelQualityScoreCache.Lock()
	defer channelQualityScoreCache.Unlock()
	for key := range channelQualityScoreCache.entries {
		if key.db == db {
			delete(channelQualityScoreCache.entries, key)
		}
	}
}

// Five-second snapshots keep repeated fallback picks from rescanning traffic.
// The database version is checked on every call, including cross-instance edits.
func queryChannelQualityScores(ctx context.Context, db *sql.DB, ids []int64, model string) (map[int64]*service.ChannelQualityScore, error) {
	if len(ids) > 1000 {
		return nil, fmt.Errorf("at most 1000 accounts can be scored at once")
	}
	result := make(map[int64]*service.ChannelQualityScore, len(ids))
	if len(ids) == 0 {
		return result, nil
	}
	c, err := getChannelQualityScoreConfig(ctx, db)
	if err != nil {
		return nil, err
	}
	model = strings.TrimSpace(model)
	now := time.Now()
	missing := make([]int64, 0, len(ids))
	seen := map[int64]bool{}
	channelQualityScoreCache.Lock()
	for _, id := range ids {
		if id <= 0 || seen[id] {
			continue
		}
		seen[id] = true
		key := channelQualityScoreCacheKey{db: db, accountID: id, model: model, version: c.Version}
		if entry, ok := channelQualityScoreCache.entries[key]; ok && now.Before(entry.expires) {
			result[id] = cloneChannelQualityScore(entry.score)
		} else {
			missing = append(missing, id)
		}
	}
	channelQualityScoreCache.Unlock()
	if len(missing) == 0 {
		return result, nil
	}
	fresh, err := queryChannelQualityScoresUncached(ctx, db, missing, model, c)
	if err != nil {
		return nil, err
	}
	channelQualityScoreCache.Lock()
	if len(channelQualityScoreCache.entries)+len(fresh) > 4096 {
		// This is only a short lived read cache; eviction never changes live holds.
		clear(channelQualityScoreCache.entries)
	}
	for id, score := range fresh {
		result[id] = cloneChannelQualityScore(score)
		key := channelQualityScoreCacheKey{db: db, accountID: id, model: model, version: c.Version}
		channelQualityScoreCache.entries[key] = channelQualityScoreCacheEntry{score: score, expires: now.Add(5 * time.Second)}
	}
	channelQualityScoreCache.Unlock()
	return result, nil
}

// Account windows are read in a single snapshot. Each account reads at most
// max_requests*4+1 usage rows and max_requests failures before deduplication; expensive
// prefix-history lookups run only for the final, capped outcome set. Only requests
// started after independent telemetry capture began are trusted; completion
// after the migration alone cannot establish complete attempt history.
func queryChannelQualityScoresUncached(ctx context.Context, db *sql.DB, accountIDs []int64, model string, c service.ChannelQualityScoreConfig) (map[int64]*service.ChannelQualityScore, error) {
	result := make(map[int64]*service.ChannelQualityScore, len(accountIDs))
	if len(accountIDs) == 0 {
		return result, nil
	}
	if len(accountIDs) > 1000 {
		return nil, fmt.Errorf("at most 1000 accounts can be scored at once")
	}
	ids := make([]int64, 0, len(accountIDs))
	for _, id := range accountIDs {
		if id <= 0 {
			continue
		}
		if _, seen := result[id]; !seen {
			ids = append(ids, id)
			result[id] = nil
		}
	}
	if len(ids) == 0 {
		return result, nil
	}
	model = strings.TrimSpace(model)
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	if err = c.Validate(); err != nil {
		return nil, err
	}
	now := time.Now()
	if !c.Enabled {
		for _, id := range ids {
			result[id] = service.SummarizeChannelQualityScore(c, nil, model, true, now)
		}
		return result, tx.Commit()
	}
	from := now.Add(-time.Duration(c.WindowSeconds) * time.Second)
	if c.EvidenceSince.After(from) {
		from = c.EvidenceSince
	}
	rows, err := tx.QueryContext(ctx, `WITH targets AS (SELECT unnest($1::bigint[]) AS account_id),
 raw_usage AS MATERIALIZED (
 SELECT u.* FROM targets a CROSS JOIN LATERAL (
  SELECT ul.* FROM usage_logs ul WHERE ul.account_id=a.account_id
  AND ul.created_at>=$3 AND ul.created_at<=$4
  AND ($2='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$2)
  ORDER BY ul.created_at DESC,ul.id DESC LIMIT $6
 ) u
 ), failed AS MATERIALIZED (
 SELECT f.* FROM targets a CROSS JOIN LATERAL (
  SELECT request_key,created_at,model,account_id FROM passive_quality_failures
  WHERE account_id=a.account_id AND ($2='' OR model=$2) AND created_at>=$3 AND created_at<=$4
  AND started_at>=$7
  ORDER BY created_at DESC,id DESC LIMIT $5
 ) f
 ), succeeded AS (
 SELECT DISTINCT ON (ul.account_id,COALESCE(NULLIF(cr.attempt_key,''),NULLIF(ul.request_id,''),'usage:'||ul.id))
 ul.*,CASE WHEN `+outputTPSEligibleSQL("ul")+` THEN ul.output_tokens ELSE 0 END AS tps_output,
 CASE WHEN `+outputTPSEligibleSQL("ul")+` THEN ul.duration_ms::float8-ul.first_token_ms::float8 ELSE 0 END AS tps_ms,
 COALESCE(NULLIF(cr.attempt_key,''),NULLIF(ul.request_id,''),'usage:'||ul.id) AS outcome_key,
 COALESCE(cr.cache_reported,false) OR ul.cache_read_tokens>0 AS cache_reported,cr.cache_metadata,
 CASE WHEN `+channelMonitorV2CacheEligibleUL+` AND (ul.cache_read_tokens>0 OR cr.cache_reported)
 AND NOT COALESCE(ul.native_compaction_v2,false)
 AND COALESCE(ul.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(ul.upstream_endpoint,'') NOT LIKE '%/compact%'
 THEN GREATEST(ul.input_tokens-ul.image_input_tokens,0)::bigint+GREATEST(ul.cache_creation_tokens,0)+GREATEST(ul.cache_read_tokens,0) ELSE 0 END AS cache_input
 FROM raw_usage ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE COALESCE(ul.request_type,0) NOT IN (4,6)
 AND cr.started_at IS NOT NULL AND cr.started_at>=$7 AND cr.started_at<=ul.created_at
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_input_tokens>0 OR ul.image_output_tokens>0 OR ul.total_cost>0 OR ul.actual_cost>0 OR ul.image_count>0 OR ul.video_count>0)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=ul.account_id
 AND f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id) AND f.created_at>=$3 AND f.created_at<=$4)
 ORDER BY ul.account_id,COALESCE(NULLIF(cr.attempt_key,''),NULLIF(ul.request_id,''),'usage:'||ul.id),ul.created_at DESC,ul.id DESC
 ), outcomes AS (
 SELECT account_id,outcome_key AS key,created_at,false AS failed,
 COALESCE(NULLIF(TRIM(requested_model),''),model) AS model,
 CASE WHEN stream OR openai_ws_mode THEN first_token_ms END::float8 AS ttft,duration_ms::float8 AS duration,
 cache_reported,cache_metadata,cache_input,GREATEST(cache_read_tokens,0)::bigint AS cache_read,tps_output,tps_ms FROM succeeded
 UNION ALL SELECT account_id,request_key,created_at,true,model,NULL::float8,NULL::float8,false,NULL::jsonb,0,0,0,0 FROM failed
 ), ranked AS (SELECT *,row_number() OVER(PARTITION BY account_id ORDER BY created_at DESC,key DESC) AS n FROM outcomes),
 selected AS MATERIALIZED (SELECT * FROM ranked WHERE n<=$5)
 SELECT ul.account_id,ul.key,ul.created_at,ul.failed,COALESCE(ul.model,''),ul.ttft,ul.duration,
 ul.cache_reported,ul.cache_input,ul.cache_read,ul.tps_output,ul.tps_ms,
 CASE WHEN COALESCE(ul.cache_metadata->>'version','')='1'
 AND COALESCE(ul.cache_metadata->>'session_hash','')<>'' AND COALESCE(ul.cache_metadata->>'prompt_hash','')<>''
 AND COALESCE(ul.cache_metadata->>'unsupported_reason','')='' AND ul.cache_metadata->>'wire_verified'='true'
 AND COALESCE(ul.cache_metadata->>'scope','')<>'' AND COALESCE(ul.cache_metadata->>'model','')<>''
 AND (ul.cache_metadata->>'started_at')::timestamptz<=ul.created_at
 AND (recent.account_id IS NULL OR recent.account_id=ul.account_id)
 AND prior.samples>=1 AND prior.tokens>=COALESCE((ul.cache_metadata->>'minimum_tokens')::bigint,1024)
 THEN true ELSE false END AS reusable,COALESCE(prior.tokens,0)::bigint,
 (SELECT count(*)<$6 FROM raw_usage ru WHERE ru.account_id=ul.account_id)
 OR ((SELECT count(*)>=$5 FROM selected so WHERE so.account_id=ul.account_id)
 AND (SELECT min(so.created_at) FROM selected so WHERE so.account_id=ul.account_id)
 > (SELECT min(ru.created_at) FROM raw_usage ru WHERE ru.account_id=ul.account_id))
 FROM selected ul
 LEFT JOIN LATERAL (
 SELECT count(*) AS samples,max(GREATEST(prev.input_tokens-prev.image_input_tokens,0)::bigint+GREATEST(prev.cache_creation_tokens,0)+GREATEST(prev.cache_read_tokens,0)) AS tokens
 FROM passive_quality_cache_reports pc JOIN usage_logs prev ON prev.account_id=pc.account_id AND prev.request_id=pc.request_key
 WHERE pc.account_id=ul.account_id AND pc.cache_metadata->>'session_hash'=ul.cache_metadata->>'session_hash'
 AND pc.cache_metadata->>'scope'=ul.cache_metadata->>'scope' AND pc.cache_metadata->>'model'=ul.cache_metadata->>'model'
 AND COALESCE(pc.cache_metadata->>'unsupported_reason','')='' AND pc.cache_metadata->>'wire_verified'='true' AND pc.cache_metadata->>'version'='1'
 AND COALESCE(pc.cache_metadata->>'scope','')<>'' AND COALESCE(pc.cache_metadata->>'model','')<>''
 AND ul.cache_metadata->'prefix_hashes' ? (pc.cache_metadata->>'prompt_hash')
 AND (pc.cache_metadata->>'started_at')::timestamptz<=prev.created_at
 AND (pc.cache_metadata->>'started_at')::timestamptz >= (ul.cache_metadata->>'started_at')::timestamptz-LEAST(300,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300),COALESCE((pc.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second'
 AND pc.created_at < (ul.cache_metadata->>'started_at')::timestamptz
 AND pc.created_at >= (ul.cache_metadata->>'started_at')::timestamptz-LEAST(300,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300),COALESCE((pc.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second'
 AND prev.created_at < (ul.cache_metadata->>'started_at')::timestamptz
 AND prev.created_at >= (ul.cache_metadata->>'started_at')::timestamptz-LEAST(300,COALESCE((ul.cache_metadata->>'retention_seconds')::int,300),COALESCE((pc.cache_metadata->>'retention_seconds')::int,300))*INTERVAL '1 second'
 AND prev.duration_ms IS NOT NULL AND COALESCE(prev.request_type,0) NOT IN (4,6)
 AND NOT COALESCE(prev.native_compaction_v2,false) AND COALESCE(prev.inbound_endpoint,'') NOT LIKE '%/compact%' AND COALESCE(prev.upstream_endpoint,'') NOT LIKE '%/compact%'
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=pc.account_id AND f.request_key=COALESCE(NULLIF(pc.attempt_key,''),pc.request_key))
 ) prior ON NOT ul.failed AND ul.cache_reported
 LEFT JOIN LATERAL (
 SELECT pc.account_id FROM passive_quality_cache_reports pc
 JOIN usage_logs prev ON prev.account_id=pc.account_id AND prev.request_id=pc.request_key
 WHERE pc.cache_metadata->>'session_hash'=ul.cache_metadata->>'session_hash'
 AND pc.cache_metadata->>'model'=ul.cache_metadata->>'model'
 AND pc.created_at<(ul.cache_metadata->>'started_at')::timestamptz AND prev.created_at<(ul.cache_metadata->>'started_at')::timestamptz
 AND pc.created_at>=(ul.cache_metadata->>'started_at')::timestamptz-INTERVAL '2 days'
 ORDER BY pc.created_at DESC LIMIT 1
 ) recent ON NOT ul.failed AND ul.cache_reported
 ORDER BY ul.account_id,ul.created_at DESC,ul.key DESC`, pq.Array(ids), model, from, now, c.MaxRequests, c.MaxRequests*4+1, c.EvidenceSince)
	if err != nil {
		return nil, err
	}
	observations := make(map[int64][]service.ChannelQualityScoreObservation, len(ids))
	withinBudget := make(map[int64]bool, len(ids))
	for rows.Next() {
		var id int64
		var v service.ChannelQualityScoreObservation
		var budget bool
		if err = rows.Scan(&id, &v.Key, &v.At, &v.Failed, &v.Model, &v.TTFT, &v.Duration, &v.CacheReported, &v.CacheInput, &v.CacheRead, &v.OutputTokens, &v.GenerationMS, &v.CacheReusable, &v.CacheExpectedTokens, &budget); err != nil {
			_ = rows.Close()
			return nil, err
		}
		observations[id] = append(observations[id], v)
		withinBudget[id] = budget
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	// Gaps are evaluated with the same transaction/window as outcomes. One
	// batch query avoids N additional round trips in the fallback hot path.
	rows, err = tx.QueryContext(ctx, `SELECT a.account_id,
 NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gaps WHERE created_at>=$2 AND created_at<=$3)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_telemetry_gap_scopes g WHERE g.created_at<=$3 AND g.ended_at>=$2
 AND (g.account_id=0 OR g.account_id=a.account_id) AND ($4='' OR g.model='' OR g.model=$4))
 -- Silently dropping successes with an unknown start would bias the error
 -- rate upward. Keep that account's bounded snapshot explicitly incomplete.
 AND NOT EXISTS(SELECT 1 FROM (
  SELECT ul.* FROM usage_logs ul WHERE ul.account_id=a.account_id AND ul.created_at>=$2 AND ul.created_at<=$3
  AND ($4='' OR COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model)=$4)
  ORDER BY ul.created_at DESC,ul.id DESC LIMIT $5
 ) ul LEFT JOIN passive_quality_cache_reports cr ON cr.account_id=ul.account_id AND cr.request_key=ul.request_id
 WHERE (cr.started_at IS NULL OR cr.started_at>ul.created_at) AND COALESCE(ul.request_type,0) NOT IN (4,6)
 AND (ul.input_tokens>0 OR ul.output_tokens>0 OR ul.cache_creation_tokens>0 OR ul.cache_read_tokens>0 OR ul.image_input_tokens>0 OR ul.image_output_tokens>0 OR ul.total_cost>0 OR ul.actual_cost>0 OR ul.image_count>0 OR ul.video_count>0)
 AND NOT EXISTS(SELECT 1 FROM passive_quality_failures f WHERE f.account_id=ul.account_id
 AND f.request_key=COALESCE(NULLIF(cr.attempt_key,''),ul.request_id) AND f.created_at>=$2 AND f.created_at<=$3))
 FROM unnest($1::bigint[]) AS a(account_id)`, pq.Array(ids), from, now, model, c.MaxRequests*4+1)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id int64
		var complete bool
		if err = rows.Scan(&id, &complete); err != nil {
			_ = rows.Close()
			return nil, err
		}
		budget, seen := withinBudget[id]
		result[id] = service.SummarizeChannelQualityScore(c, observations[id], model, complete && (!seen || budget), now)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	return result, tx.Commit()
}

func cloneChannelQualityScore(source *service.ChannelQualityScore) *service.ChannelQualityScore {
	if source == nil {
		return nil
	}
	copy := *source
	if source.Value != nil {
		value := *source.Value
		copy.Value = &value
	}
	copy.Dimensions = append([]service.ChannelQualityScoreDimension{}, source.Dimensions...)
	for i := range copy.Dimensions {
		if source.Dimensions[i].Value != nil {
			value := *source.Dimensions[i].Value
			copy.Dimensions[i].Value = &value
		}
		if source.Dimensions[i].Score != nil {
			value := *source.Dimensions[i].Score
			copy.Dimensions[i].Score = &value
		}
	}
	return &copy
}
