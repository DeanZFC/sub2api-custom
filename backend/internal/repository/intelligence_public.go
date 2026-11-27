package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.PublicIntelligenceRepository = (*scheduledTestResultRepository)(nil)

// Qualify the source group against today's entitlements. Joining current
// account_groups here would move historical results to another user's pool.
const intelligenceEntitledGroups = `SELECT g.id,g.name FROM groups g
 WHERE g.deleted_at IS NULL AND g.status='active' AND (
 (NOT g.is_exclusive AND g.subscription_type<>'subscription')
 OR EXISTS(SELECT 1 FROM user_allowed_groups ag WHERE ag.user_id=$1 AND ag.group_id=g.id)
 OR EXISTS(SELECT 1 FROM user_subscriptions s WHERE s.user_id=$1 AND s.group_id=g.id
 AND s.deleted_at IS NULL AND s.status='active' AND s.starts_at<=NOW() AND s.expires_at>NOW()))`

// Read the opt-in in the same database snapshot as the result, including direct
// requests for a previously visible artwork or round.
const intelligenceRoundsVisibleSQL = `EXISTS (SELECT 1 FROM settings WHERE key='` + service.SettingKeyIntelligenceRoundsVisible + `' AND value='true')`
const intelligencePlanVisible = intelligenceRoundsVisibleSQL + ` AND p.enabled AND jsonb_typeof(p.protection->'intelligence')='object'`

func (r *scheduledTestResultRepository) PublicIntelligenceDirectory(ctx context.Context, user int64) (*service.PublicIntelligenceDirectory, error) {
	// The established legacy projector performs account and plan visibility checks.
	// Selecting just the group ID lets Postgres omit all output and diagnostic fields.
	var dir service.PublicIntelligenceDirectory
	var raw []byte
	err := r.db.QueryRowContext(ctx, scheduledTestVisibleProjectionCTE+scheduledTestVisibleAccessCTE+`,
 legacy AS (SELECT DISTINCT group_id FROM visible_results WHERE status IN ('success','passed','failed','error')),
 entitled_groups AS (`+intelligenceEntitledGroups+`),
 selected AS (SELECT g.id, models.model FROM entitled_groups g
 JOIN scheduled_test_plans p ON g.id=ANY(p.group_ids) AND `+intelligencePlanVisible+`
 CROSS JOIN LATERAL unnest(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END) models(model)), visible_groups AS (
 SELECT g.id,g.name,COALESCE(array_agg(DISTINCT sel.model ORDER BY sel.model) FILTER (WHERE sel.model IS NOT NULL),'{}'::text[]) AS models,
 l.group_id IS NOT NULL AS has_legacy_results FROM entitled_groups g LEFT JOIN selected sel ON sel.id=g.id LEFT JOIN legacy l ON l.group_id=g.id
 WHERE sel.id IS NOT NULL OR l.group_id IS NOT NULL GROUP BY g.id,g.name,l.group_id)
 SELECT `+intelligenceRoundsVisibleSQL+`, COALESCE(json_agg(g ORDER BY g.name,g.id),'[]'::json) FROM visible_groups g`, user).Scan(&dir.RoundsVisible, &raw)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &dir.Groups); err != nil {
		return nil, err
	}
	return &dir, nil
}

// Selecting the run before loading the models preserves the group's completion
// boundary even when a viewer filters down to a model that finished early.
// For each model, an account contributes its worst conclusive result across
// efforts. Keep the full severity separately for the account detail and summary.
const publicIntelligenceWindow = `WITH entitled AS (` + intelligenceEntitledGroups + `),
 recent AS MATERIALIZED (SELECT b.run_id,b.started_at,b.finished_at
 FROM intelligence_rounds b JOIN scheduled_test_plans p ON p.id=b.plan_id
 JOIN entitled eg ON eg.id=$2 AND eg.id=ANY(p.group_ids)
 WHERE ` + intelligencePlanVisible + ` AND b.finished_at IS NOT NULL
 AND EXISTS(SELECT 1 FROM intelligence_round_targets t WHERE t.run_id=b.run_id AND t.group_id=$2 AND ($3='' OR t.model=$3))
 ORDER BY b.finished_at DESC,b.run_id DESC LIMIT 20),
 targets AS NOT MATERIALIZED (SELECT b.run_id,b.started_at,b.finished_at,t.alias,t.model,t.effort,
 r.id AS result_id,r.finished_at AS tested_at,r.execution_info->'intelligence' AS evidence,
 CASE WHEN COALESCE(r.execution_info->'intelligence'->>'auto_verdict','unknown')<>'pass' THEN 'skipped'
 WHEN r.execution_info->'intelligence'->>'artwork' IS DISTINCT FROM 'default' OR octet_length(r.output_html)=0 THEN 'missing'
 WHEN rv.verdict='fail' THEN 'veto' WHEN rv.verdict='pass' THEN 'approved' ELSE 'default' END AS artwork,
 CASE WHEN r.status IN ('failed','error') THEN 1
 WHEN r.execution_info->'intelligence'->>'auto_verdict'='confirmed' THEN 3
 WHEN r.execution_info->'intelligence'->>'auto_verdict'='suspect' THEN 2
 WHEN r.execution_info->'intelligence'->>'auto_verdict' IS DISTINCT FROM 'pass' THEN 1
 WHEN r.execution_info->'intelligence'->>'artwork' IS DISTINCT FROM 'default' OR octet_length(r.output_html)=0 THEN 1
 WHEN rv.verdict='fail' THEN 3 ELSE 0 END AS severity
 FROM recent b JOIN intelligence_round_targets t ON t.run_id=b.run_id AND t.group_id=$2 AND ($3='' OR t.model=$3)
 LEFT JOIN scheduled_test_results r ON r.id=t.result_id
 LEFT JOIN intelligence_artwork_reviews rv ON rv.result_id=t.result_id),
 model_accounts AS (SELECT run_id,alias,model,MAX(severity) AS severity,
 MAX(severity) FILTER(WHERE severity IN (0,3)) AS decided_severity,
 count(*) AS targets,count(*) FILTER(WHERE severity=0) AS passed
 FROM targets GROUP BY run_id,alias,model),
 accounts AS (SELECT run_id,alias,MAX(severity) AS severity,sum(targets) AS targets,sum(passed) AS passed
 FROM model_accounts GROUP BY run_id,alias)`
const intellCountSQL = `count(*) FILTER(WHERE a.severity=0),count(*) FILTER(WHERE a.severity=2),count(*) FILTER(WHERE a.severity=3),count(*) FILTER(WHERE a.severity=1)`
const intellRoundSQL = `, model_counts AS (
 SELECT run_id,model,count(*) FILTER(WHERE decided_severity=0) AS pass,count(*) FILTER(WHERE decided_severity=3) AS degraded
 FROM model_accounts GROUP BY run_id,model)
 SELECT b.run_id,b.started_at,b.finished_at,MAX(a.severity),` + intellCountSQL + `,count(*),sum(a.targets),sum(a.passed),
 (SELECT json_agg(json_build_object('model',m.model,'pass',m.pass,'degraded',m.degraded) ORDER BY m.model)
  FROM model_counts m WHERE m.run_id=b.run_id)
 FROM recent b JOIN accounts a ON a.run_id=b.run_id`
const intellRoundGroup = ` GROUP BY b.run_id,b.started_at,b.finished_at ORDER BY b.finished_at,b.run_id`

func publicIntelligenceVerdict(severity int) string {
	return [...]string{"pass", "unknown", "suspect", "degraded"}[severity]
}
func scanIntelligenceRound(row scannable) (service.PublicIntelligenceRound, error) {
	var b service.PublicIntelligenceRound
	var severity int
	var models []byte
	if err := row.Scan(&b.ID, &b.StartedAt, &b.FinishedAt, &severity, &b.Counts.Pass, &b.Counts.Suspect, &b.Counts.Degraded, &b.Counts.Unknown, &b.Accounts, &b.Targets, &b.PassedTargets, &models); err != nil {
		return b, err
	}
	b.Verdict = publicIntelligenceVerdict(severity)
	err := json.Unmarshal(models, &b.ModelCounts)
	return b, err
}
func (r *scheduledTestResultRepository) PublicIntelligenceRounds(ctx context.Context, user, group int64, model string) ([]service.PublicIntelligenceRound, error) {
	rows, err := r.db.QueryContext(ctx, publicIntelligenceWindow+intellRoundSQL+intellRoundGroup, user, group, model)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []service.PublicIntelligenceRound{}
	for rows.Next() {
		b, err := scanIntelligenceRound(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, rows.Err()
}

func publicChecks(raw []byte) ([]service.PublicIntelligenceCheck, error) {
	var data struct {
		Checks         []service.PublicIntelligenceCheck `json:"checks"`
		Consecutive429 int                               `json:"consecutive_429"`
	}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &data); err != nil {
			return nil, err
		}
	}
	recorded := make(map[string]service.PublicIntelligenceCheck, len(data.Checks))
	for _, check := range data.Checks {
		recorded[check.Key] = check
	}
	out := make([]service.PublicIntelligenceCheck, 0, 4)
	for _, k := range []string{"fingerprint", "codex_state", "candy", "http_429"} {
		c, ok := recorded[k]
		// Only historical rounds can have the two retired checks.
		if !ok && (k == "fingerprint" || k == "codex_state") {
			continue
		}
		if !ok || (c.Status != "pass" && c.Status != "fail" && c.Status != "unknown") {
			c = service.PublicIntelligenceCheck{Key: k, Status: "unknown"}
		}
		if k == "http_429" {
			c.Key = "rate_limit"
			if data.Consecutive429 > 0 {
				c.Detail = fmt.Sprintf("%d/3", data.Consecutive429)
			}
		}
		out = append(out, c)
	}
	return out, nil
}

func (r *scheduledTestResultRepository) PublicIntelligenceRoundDetails(ctx context.Context, user, group int64, model, run string, page int) (*service.PublicIntelligenceRoundDetails, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	header, err := scanIntelligenceRound(tx.QueryRowContext(ctx, publicIntelligenceWindow+intellRoundSQL+` WHERE b.run_id=$4`+intellRoundGroup, user, group, model, run))
	if err != nil {
		return nil, err
	}
	out := &service.PublicIntelligenceRoundDetails{Round: header, Accounts: []service.PublicIntelligenceAccount{}, Page: page, PageSize: service.PublicIntelligencePageSize, Total: header.Accounts}
	rows, err := tx.QueryContext(ctx, publicIntelligenceWindow+`, page_accounts AS (
 SELECT * FROM accounts WHERE run_id=$4 ORDER BY severity DESC,alias LIMIT $5 OFFSET $6)
 SELECT a.alias,a.severity,a.targets,a.passed,t.model,t.effort,t.tested_at,t.severity,t.evidence,t.artwork,
 CASE WHEN t.artwork IN ('default','approved','veto') THEN t.result_id END
 FROM page_accounts a JOIN targets t ON t.run_id=a.run_id AND t.alias=a.alias ORDER BY a.severity DESC,a.alias,t.model,t.effort`, user, group, model, run, out.PageSize, (page-1)*out.PageSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a service.PublicIntelligenceAccount
		var m service.PublicIntelligenceModel
		var accountSeverity, modelSeverity int
		var raw []byte
		if err := rows.Scan(&a.Alias, &accountSeverity, &a.Targets, &a.PassedTargets, &m.Model, &m.Effort, &m.FinishedAt, &modelSeverity, &raw, &m.ArtworkStatus, &m.ArtworkID); err != nil {
			return nil, err
		}
		m.Verdict = publicIntelligenceVerdict(modelSeverity)
		m.Checks, err = publicChecks(raw)
		if err != nil {
			return nil, err
		}
		if len(out.Accounts) == 0 || out.Accounts[len(out.Accounts)-1].Alias != a.Alias {
			a.Verdict = publicIntelligenceVerdict(accountSeverity)
			out.Accounts = append(out.Accounts, a)
		}
		v := &out.Accounts[len(out.Accounts)-1]
		v.Models = append(v.Models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
func (r *scheduledTestResultRepository) PublicIntelligenceSummary(ctx context.Context, user, group int64, model string, page int) (*service.PublicIntelligenceSummary, error) {
	tx, err := r.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true, Isolation: sql.LevelRepeatableRead})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out := &service.PublicIntelligenceSummary{Page: page, PageSize: service.PublicIntelligencePageSize, Items: []service.PublicIntelligenceAccountStats{}}
	err = tx.QueryRowContext(ctx, publicIntelligenceWindow+` SELECT count(DISTINCT a.run_id),count(DISTINCT a.alias),count(*),`+intellCountSQL+`,COALESCE(sum(a.targets),0),COALESCE(sum(a.passed),0) FROM accounts a`, user, group, model).Scan(&out.Rounds, &out.Accounts, &out.Participations, &out.Counts.Pass, &out.Counts.Suspect, &out.Counts.Degraded, &out.Counts.Unknown, &out.Targets, &out.PassedTargets)
	if err != nil {
		return nil, err
	}
	out.Total = out.Accounts
	rows, err := tx.QueryContext(ctx, publicIntelligenceWindow+`, page_accounts AS (SELECT a.alias,count(*) AS participations,
 count(*) FILTER(WHERE a.severity=0) AS passed,count(*) FILTER(WHERE a.severity=2) AS suspect,
 count(*) FILTER(WHERE a.severity=3) AS degraded,count(*) FILTER(WHERE a.severity=1) AS unknown
 FROM accounts a GROUP BY a.alias ORDER BY a.alias LIMIT $4 OFFSET $5)
 SELECT p.alias,p.participations,p.passed,p.suspect,p.degraded,p.unknown,a.model,a.effort,`+intellCountSQL+`,max(a.tested_at)
 FROM page_accounts p JOIN targets a ON a.alias=p.alias GROUP BY p.alias,p.participations,p.passed,p.suspect,p.degraded,p.unknown,a.model,a.effort
 ORDER BY p.alias,a.model,a.effort`, user, group, model, out.PageSize, (page-1)*out.PageSize)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var a service.PublicIntelligenceAccountStats
		var m service.PublicIntelligenceModelStats
		if err := rows.Scan(&a.Alias, &a.Participations, &a.Counts.Pass, &a.Counts.Suspect, &a.Counts.Degraded, &a.Counts.Unknown, &m.Model, &m.Effort, &m.Counts.Pass, &m.Counts.Suspect, &m.Counts.Degraded, &m.Counts.Unknown, &m.LastTestedAt); err != nil {
			return nil, err
		}
		if len(out.Items) == 0 || out.Items[len(out.Items)-1].Alias != a.Alias {
			out.Items = append(out.Items, a)
		}
		entry := &out.Items[len(out.Items)-1]
		entry.Models = append(entry.Models, m)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, tx.Commit()
}
func (r *scheduledTestResultRepository) PublicIntelligenceArtwork(ctx context.Context, user, group, result int64) (string, error) {
	var markup string
	err := r.db.QueryRowContext(ctx, `WITH entitled AS (`+intelligenceEntitledGroups+`)
 SELECT r.output_html FROM intelligence_round_targets t JOIN intelligence_rounds b ON b.run_id=t.run_id
 JOIN scheduled_test_plans p ON p.id=b.plan_id JOIN entitled eg ON eg.id=t.group_id AND eg.id=ANY(p.group_ids)
 JOIN scheduled_test_results r ON r.id=t.result_id WHERE t.group_id=$2 AND t.result_id=$3
 AND `+intelligencePlanVisible+` AND b.finished_at IS NOT NULL AND r.output_kind='html'
 AND r.execution_info->'intelligence'->>'auto_verdict'='pass' AND r.execution_info->'intelligence'->>'artwork'='default'
 AND octet_length(r.output_html)>0`, user, group, result).Scan(&markup)
	return markup, err
}
