package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

type passiveModelTarget struct {
	account int64
	model   string
}

// Discover only concrete requested models. Retain durable lifecycle rows even
// after traffic stops so paused models can still probe and recover. Each source
// branch and sweep batch is bounded; the oldest state is processed first.
func (r *passiveQualityRepository) modelTargets(ctx context.Context, p service.PassiveQualityPolicy, now time.Time) ([]passiveModelTarget, error) {
	window := p.WindowSeconds
	if p.CacheProtection != nil {
		window = max(window, p.CacheProtection.Quick.WindowSeconds, p.CacheProtection.ActionWindow.WindowSeconds)
	}
	from := now.Add(-time.Duration(window) * time.Second)
	if p.EvidenceSince().After(from) {
		from = p.EvidenceSince()
	}
	groupID := int64(0)
	if p.IsGroupQualityTarget() {
		groupID = p.GroupIDs[0]
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.id,m.model FROM accounts a
 CROSS JOIN LATERAL (
 SELECT $4::text AS model WHERE NOT $5 OR $4<>''
 UNION SELECT model_key FROM passive_quality_states WHERE $5 AND $4='' AND policy_id=$2 AND account_id=a.id AND model_key<>''
 UNION SELECT model FROM (SELECT COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model) AS model FROM usage_logs ul
 WHERE $5 AND $4='' AND ul.account_id=a.id AND ul.created_at>=$6 AND ul.created_at<=$7 AND ($8::bigint=0 OR (ul.group_id=$8 AND `+passiveGroupTargetTextUsageSQL+`))
 AND COALESCE(ul.request_type,0) NOT IN (4,6) AND ul.duration_ms IS NOT NULL
 ORDER BY ul.created_at DESC,ul.id DESC LIMIT 10001) u WHERE COALESCE(TRIM(model),'')<>''
 UNION SELECT model FROM (SELECT f.model FROM passive_quality_failures f WHERE $5 AND $4=''
 AND f.account_id=a.id AND f.created_at>=$6 AND f.created_at<=$7 AND ($8::bigint=0 OR (f.group_id=$8 AND NOT f.non_text)) ORDER BY f.created_at DESC,f.id DESC LIMIT 10001) f WHERE TRIM(model)<>''
 ) m LEFT JOIN passive_quality_states s ON s.account_id=a.id AND s.policy_id=$2 AND s.model_key=CASE WHEN $5 THEN m.model ELSE '' END
 WHERE a.deleted_at IS NULL AND ((NOT(a.id=ANY($3)) AND EXISTS(SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id WHERE ag.account_id=a.id AND ag.group_id=ANY($1) AND g.status='active' AND g.deleted_at IS NULL)) OR s.account_id IS NOT NULL)
 ORDER BY s.data->>'updated_at' ASC NULLS FIRST,a.id,m.model LIMIT 128`, pq.Array(p.GroupIDs), p.ID, pq.Array(p.ExcludedAccountIDs), p.Model, p.PerModel(), from, now, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	targets := []passiveModelTarget{}
	for rows.Next() {
		var t passiveModelTarget
		if err = rows.Scan(&t.account, &t.model); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

func passiveModelKey(p service.PassiveQualityPolicy, s service.PassiveQualityState) string {
	if p.PerModel() {
		return s.Model
	}
	return ""
}

// Replays are capped independently of account count because automatic model
// discovery adds another dimension. No unknown-model account bucket is created.
func passiveReplayModels(ctx context.Context, tx *sql.Tx, p service.PassiveQualityPolicy, aid int64, from, to time.Time) ([]string, error) {
	if !p.PerModel() || p.Model != "" {
		return []string{p.Model}, nil
	}
	groupID := int64(0)
	if p.IsGroupQualityTarget() {
		groupID = p.GroupIDs[0]
	}
	rows, err := tx.QueryContext(ctx, `SELECT model FROM (
 SELECT COALESCE(NULLIF(TRIM(ul.requested_model),''),ul.model) AS model FROM usage_logs ul WHERE ul.account_id=$1 AND ul.created_at>=$2 AND ul.created_at<=$3 AND COALESCE(ul.request_type,0) NOT IN (4,6) AND ($4::bigint=0 OR (ul.group_id=$4 AND `+passiveGroupTargetTextUsageSQL+`))
 UNION SELECT model FROM passive_quality_failures WHERE account_id=$1 AND created_at>=$2 AND created_at<=$3 AND ($4::bigint=0 OR (group_id=$4 AND NOT non_text))
 ) x WHERE COALESCE(TRIM(model),'')<>'' ORDER BY model LIMIT 129`, aid, from, to, groupID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	models := []string{}
	for rows.Next() {
		var m string
		if err = rows.Scan(&m); err != nil {
			return nil, err
		}
		models = append(models, m)
	}
	return models, rows.Err()
}
