package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

func advancePassiveMetrics(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, s *service.PassiveQualityState, now time.Time) ([]string, error) {
	events := []string{}
	if p.IsGroupQualityTarget() {
		return events, nil
	}
	if p.TTFT.Enabled {
		if s.Latency == nil {
			s.Latency = &service.PassiveQualityMetricState{ObserveAfter: p.UpdatedAt}
		}
		view := *s
		view.Phase = "healthy"
		view.ObserveAfter = s.Latency.ObserveAfter
		view.LastEvidenceAt = s.Latency.LastEvidenceAt
		if s.CacheObserveAfter.After(view.ObserveAfter) {
			view.ObserveAfter = s.CacheObserveAfter
		}
		e, err := passiveSamples(ctx, tx, p, view, now)
		if err != nil {
			return nil, err
		}
		if event := service.AdvancePassiveQualityLatency(p.PassiveQualityConfig, s.Latency, e, now); event != "" {
			events = append(events, event)
		}
	}
	if p.CacheRate.Enabled && p.CacheProtection != nil {
		samples, complete, err := passiveCacheSamples(ctx, tx, p, *s, now)
		if err != nil {
			return nil, err
		}
		if s.Cache == nil {
			s.Cache = map[string]service.PassiveQualityCacheState{}
		}
		windows := service.PassiveQualityCacheWindows(p.PassiveQualityConfig, samples, s.Cache, now, complete)
		for model, e := range windows {
			state := s.Cache[model]
			allow := false
			if !s.OtherHold && s.Phase != "paused" && s.Phase != "probing" && s.Phase != "trial" && p.Mode == "enforce" && p.CacheProtection.Action == "reduce" {
				preview := state
				if service.AdvancePassiveQualityCache(p.PassiveQualityConfig, &preview, e, now, true) == "cache_reduced" {
					allow, err = passiveCachePoolAllows(ctx, tx, p, s.AccountID, model, now)
					if err != nil {
						return nil, err
					}
				}
			}
			if event := service.AdvancePassiveQualityCache(p.PassiveQualityConfig, &state, e, now, allow); event != "" {
				events = append(events, event)
			}
			s.Cache[model] = state
		}
	}
	return events, nil
}

// The caller holds the shared expansion lock. Count distinct accounts across
// all overlapping policies, and enforce the strictest policy on each group.
// Hard-unavailable accounts never contribute to the serving-capacity reserve.
func passiveCachePoolAllows(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, aid int64, model string, now time.Time) (bool, error) {
	rows, err := tx.QueryContext(ctx, `WITH affected_groups AS (
 SELECT DISTINCT g.id FROM account_groups ag JOIN groups g ON g.id=ag.group_id
 WHERE ag.account_id=$2 AND g.deleted_at IS NULL AND g.status='active'
 AND EXISTS(SELECT 1 FROM account_groups target WHERE target.account_id=$2 AND target.group_id=ANY($1))
 ), members AS (
 SELECT DISTINCT ag.group_id,a.id,to_jsonb(a) AS account,
 EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE s.account_id=a.id AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce' AND `+passiveMembership+`
 AND s.data->'cache'->$3->>'phase' IN ('reduced','recovering')) AS reduced
 FROM accounts a JOIN account_groups ag ON ag.account_id=a.id
 JOIN groups g ON g.id=ag.group_id
 WHERE ag.group_id IN (SELECT id FROM affected_groups) AND g.deleted_at IS NULL AND g.status='active'
 AND a.deleted_at IS NULL AND a.status='active' AND a.schedulable
 AND NOT COALESCE(a.temp_unschedulable_until>$4,false) AND NOT COALESCE(a.rate_limit_reset_at>$4,false)
 AND NOT COALESCE((to_jsonb(a)->>'overload_until')::timestamptz>$4,false)
 AND NOT (COALESCE((to_jsonb(a)->>'auto_pause_on_expired')::boolean,false) AND COALESCE((to_jsonb(a)->>'expires_at')::timestamptz<=$4,false))
 AND NOT EXISTS(SELECT 1 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE s.account_id=a.id AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce' AND `+passiveMembership+`
 AND s.data->>'phase' IN ('paused','probing','trial') AND (p.config->>'scope'='account' OR COALESCE(NULLIF(s.model_key,''),p.config->>'model')=$3))
 ), caps AS (SELECT g.id,
 LEAST($5,COALESCE(min((q.config->'cache_protection'->>'max_reduced_percent')::int),$5)) AS percent,
 GREATEST($6,COALESCE(max((q.config->'cache_protection'->>'min_serving_accounts')::int),$6)) AS reserve
 FROM groups g LEFT JOIN passive_quality_policies q ON q.config->>'enabled'='true' AND q.config->>'mode'='enforce'
 AND q.config->>'schema_version'='2' AND q.config->'cache_rate'->>'enabled'='true'
 AND q.config->'cache_protection'->>'action'='reduce'
 AND (COALESCE(q.config->>'model','')='' OR q.config->>'model'=$3)
 AND q.config->'group_ids' @> to_jsonb(ARRAY[g.id])
 WHERE g.id IN (SELECT id FROM affected_groups) GROUP BY g.id)
 SELECT c.id,m.id,m.account,COALESCE(m.reduced,false),c.percent,c.reserve
 FROM caps c LEFT JOIN members m ON m.group_id=c.id WHERE $2::bigint>0 ORDER BY c.id,m.id`, pq.Array(p.GroupIDs), aid, model, now, p.CacheProtection.MaxReducedPercent, p.CacheProtection.MinServingAccounts)
	if err != nil {
		return false, err
	}
	defer func() { _ = rows.Close() }()
	type groupCapacity struct {
		total, reduced, percent, reserve int
		member, already                  bool
	}
	groups := map[int64]*groupCapacity{}
	eligibility := map[int64]bool{}
	for rows.Next() {
		var gid int64
		var id sql.NullInt64
		var raw []byte
		var reduced bool
		var percent, reserve int
		if err = rows.Scan(&gid, &id, &raw, &reduced, &percent, &reserve); err != nil {
			return false, err
		}
		group := groups[gid]
		if group == nil {
			group = &groupCapacity{percent: percent, reserve: reserve}
			groups[gid] = group
		}
		if !id.Valid {
			continue
		}
		eligible, known := eligibility[id.Int64]
		if !known {
			// Hydrate only fields consumed by the common durable scheduling gates;
			// JSON keeps optional legacy fixture columns backward compatible.
			var row struct {
				ID                     int64          `json:"id"`
				Platform               string         `json:"platform"`
				Type                   string         `json:"type"`
				Credentials            map[string]any `json:"credentials"`
				Extra                  map[string]any `json:"extra"`
				Status                 string         `json:"status"`
				Schedulable            bool           `json:"schedulable"`
				ExpiresAt              *time.Time     `json:"expires_at"`
				AutoPauseOnExpired     bool           `json:"auto_pause_on_expired"`
				RateLimitResetAt       *time.Time     `json:"rate_limit_reset_at"`
				OverloadUntil          *time.Time     `json:"overload_until"`
				TempUnschedulableUntil *time.Time     `json:"temp_unschedulable_until"`
				ParentAccountID        *int64         `json:"parent_account_id"`
			}
			if err = json.Unmarshal(raw, &row); err != nil {
				return false, err
			}
			a := &service.Account{ID: row.ID, Platform: row.Platform, Type: row.Type, Credentials: row.Credentials, Extra: row.Extra, Status: row.Status, Schedulable: row.Schedulable, ExpiresAt: row.ExpiresAt, AutoPauseOnExpired: row.AutoPauseOnExpired, RateLimitResetAt: row.RateLimitResetAt, OverloadUntil: row.OverloadUntil, TempUnschedulableUntil: row.TempUnschedulableUntil, ParentAccountID: row.ParentAccountID}
			eligible = service.PassiveQualityPoolAccountEligible(ctx, a, model)
			eligibility[id.Int64] = eligible
		}
		if !eligible {
			continue
		}
		group.total++
		if reduced {
			group.reduced++
		}
		if id.Int64 == aid {
			group.member = true
			group.already = reduced
		}
	}
	if err = rows.Err(); err != nil {
		return false, err
	}
	seen := false
	for _, group := range groups {
		if !group.member {
			continue
		}
		seen = true
		next := group.reduced
		if !group.already {
			next++
		}
		if next*100 > group.total*group.percent || group.total-next < group.reserve {
			return false, nil
		}
	}
	return seen, nil
}
