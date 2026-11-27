package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// hold_version is retained while a model is recovering, even across policy edits.
// A review of an old artwork can change its display verdict without changing routing_veto.
const intelligenceHoldObservationSQL = `SELECT o.result_id,o.finished_at,o.routing_veto
 FROM intelligence_observations o
 WHERE s.blocked AND o.plan_id=s.plan_id AND o.account_id=s.account_id AND o.model_key=s.model_key
 AND o.policy_version=s.hold_version AND NOT o.released AND o.auto_isolate AND (o.verdict='confirmed' OR o.routing_veto)
 ORDER BY o.started_at DESC,o.result_id DESC LIMIT 1`

const intelligenceHoldRoundJoin = ` LEFT JOIN LATERAL (
 SELECT jsonb_build_object('result_id',o.result_id,'finished_at',o.finished_at,
   'checks',r.execution_info->'intelligence'->'checks','artwork_veto',o.routing_veto,
   'review_reason',CASE WHEN o.routing_veto AND v.verdict='fail' THEN v.reason END) AS evidence
 FROM (` + intelligenceHoldObservationSQL + `) o
 LEFT JOIN scheduled_test_results r ON r.id=o.result_id
 LEFT JOIN intelligence_artwork_reviews v ON v.result_id=o.result_id
 ) hold_round ON true `

func (r *scheduledTestResultRepository) IntelligenceOverview(ctx context.Context, planID int64) (*service.IntelligenceAdminOverview, error) {
	out := &service.IntelligenceAdminOverview{Results: []service.IntelligenceAdminResult{}, States: []service.IntelligenceAdminState{}}
	rows, err := r.db.QueryContext(ctx, `SELECT o.result_id,o.account_id,a.name,o.model_key,
 CASE WHEN v.verdict='fail' THEN 'confirmed' ELSE o.verdict END,
 r.execution_info->'intelligence'->>'artwork',COALESCE(v.verdict,''),o.finished_at,
 (r.status='failed' AND p.enabled AND NOT o.released AND NOT r.protection_revoked
  AND o.policy_version=p.protection->'intelligence'->>'version'
  AND r.execution_info->'intelligence'->>'artwork'='missing'),
 r.execution_info->'intelligence',COALESCE(v.reason,''),COALESCE(r.error_message,'')
 FROM intelligence_observations o JOIN scheduled_test_plans p ON p.id=o.plan_id
 JOIN scheduled_test_results r ON r.id=o.result_id JOIN accounts a ON a.id=o.account_id
 LEFT JOIN intelligence_artwork_reviews v USING(result_id)
 WHERE o.plan_id=$1 ORDER BY o.started_at DESC,o.result_id DESC LIMIT 200`, planID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var h service.IntelligenceAdminResult
		var raw []byte
		if err = rows.Scan(&h.ResultID, &h.AccountID, &h.AccountName, &h.Model, &h.Verdict, &h.Artwork, &h.Reviewed, &h.FinishedAt, &h.RetryableArtwork, &raw, &h.ReviewReason, &h.ErrorMessage); err != nil {
			_ = rows.Close()
			return nil, err
		}
		if len(raw) > 0 {
			if err = json.Unmarshal(raw, &h.Evidence); err != nil {
				_ = rows.Close()
				return nil, err
			}
		}
		out.Results = append(out.Results, h)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return nil, err
	}
	rows, err = r.db.QueryContext(ctx, `SELECT s.account_id,a.name,s.model_key,s.blocked,s.passes,s.verdict,s.next_check,hold_round.evidence
 FROM intelligence_model_states s JOIN accounts a ON a.id=s.account_id JOIN scheduled_test_plans p ON p.id=s.plan_id
 `+intelligenceHoldRoundJoin+` WHERE s.plan_id=$1 AND s.model_key=ANY(p.model_ids) AND NOT(s.account_id=ANY(p.excluded_account_ids)) AND (s.blocked OR s.verdict='suspect') ORDER BY s.next_check NULLS LAST,a.name,s.model_key`, planID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var s service.IntelligenceAdminState
		var raw []byte
		if err = rows.Scan(&s.AccountID, &s.AccountName, &s.Model, &s.Blocked, &s.Passes, &s.Verdict, &s.NextCheck, &raw); err != nil {
			return nil, err
		}
		if len(raw) > 0 {
			if err := json.Unmarshal(raw, &s.HoldRound); err != nil {
				return nil, err
			}
		}
		out.States = append(out.States, s)
	}
	return out, rows.Err()
}
func (r *scheduledTestResultRepository) IntelligenceArtwork(ctx context.Context, id int64) (string, error) {
	var html string
	err := r.db.QueryRowContext(ctx, `SELECT r.output_html FROM scheduled_test_results r JOIN intelligence_observations o ON r.id=o.result_id WHERE r.id=$1 AND r.execution_info->'intelligence'->>'artwork'='default' AND r.output_html<>''`, id).Scan(&html)
	return html, err
}

// Fence in-flight results before dropping this owner's hold. A later round can
// still establish a new hold without changing any historical review.
func (r *scheduledTestResultRepository) ReleaseIntelligenceHold(ctx context.Context, planID, accountID int64, model string, admin int64) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	cfg, _, err := intelligencePlanTx(ctx, tx, planID)
	if err != nil {
		return err
	}
	if cfg == nil {
		return sql.ErrNoRows
	}
	if _, err = lockProtectionAccount(ctx, tx, accountID); err != nil {
		return err
	}
	changed, err := tx.ExecContext(ctx, `DELETE FROM intelligence_model_states WHERE plan_id=$1 AND account_id=$2 AND model_key=$3 AND blocked`, planID, accountID, model)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return sql.ErrNoRows
	}
	if _, err = tx.ExecContext(ctx, `UPDATE scheduled_test_results SET protection_revoked=true WHERE plan_id=$1 AND account_id=$2 AND model_id=$3 AND status IN ('pending','running')`, planID, accountID, model); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE intelligence_observations SET released=true WHERE plan_id=$1 AND account_id=$2 AND model_key=$3 AND NOT released`, planID, accountID, model); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO intelligence_release_log(plan_id,account_id,model_key,admin_id) VALUES($1,$2,$3,$4)`, planID, accountID, model, admin); err != nil {
		return err
	}
	if err = reconcileProtectionAccount(ctx, tx, accountID); err != nil {
		return err
	}
	return tx.Commit()
}
