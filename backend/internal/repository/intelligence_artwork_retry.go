package repository

import (
	"context"
	"time"
)

const intelligenceEligibilityFrom = ` FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
 JOIN accounts a ON a.id=r.account_id WHERE p.enabled AND r.run_id<>'' AND NOT r.protection_revoked
 AND NOT EXISTS(SELECT 1 FROM intelligence_observations obs WHERE obs.result_id=r.id AND obs.released)
 AND NOT (a.auto_pause_on_expired AND a.expires_at IS NOT NULL AND a.expires_at<=NOW())
 AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
 AND NOT(a.id=ANY(p.excluded_account_ids)) AND r.model_id=ANY(p.model_ids)
 AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(p.group_ids))`

// A finished historic round may still need its original work. Recheck current
// membership and policy version, as well as the immutable target identity.
func (r *scheduledTestResultRepository) IntelligenceTargetEligible(ctx context.Context, id, plan, account int64, version string) (bool, error) {
	var ok bool
	err := r.db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1`+intelligenceEligibilityFrom+`
 AND r.id=$1 AND p.id=$2 AND a.id=$3 AND p.protection->'intelligence'->>'version'=$4)`, id, plan, account, version).Scan(&ok)
	return ok, err
}

func (r *scheduledTestResultRepository) DueIntelligenceArtwork(ctx context.Context, now time.Time) ([]int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT r.id`+intelligenceEligibilityFrom+`
 AND r.status='failed' AND r.execution_info->'intelligence'->>'artwork'='missing'
 AND r.execution_info->'intelligence'->>'auto_verdict'='pass'
 AND r.execution_info->'intelligence'->>'policy_version'=p.protection->'intelligence'->>'version'
 AND (r.execution_info->'intelligence'->>'artwork_retry_at')::timestamptz <= $1
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_execution_leases l WHERE l.plan_id=p.id AND l.expires_at>$1
 AND (l.scope='plan' OR l.scope='retry:'||a.id::text||':'||COALESCE(r.test_definition_id,0)::text||':'||r.model_id))
 ORDER BY (r.execution_info->'intelligence'->>'artwork_retry_at')::timestamptz,r.finished_at,r.id LIMIT 20`, now)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err = rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func (r *scheduledTestResultRepository) DeferIntelligenceArtwork(ctx context.Context, id int64, due, next time.Time) error {
	_, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_results
 SET execution_info=jsonb_set(execution_info, '{intelligence,artwork_retry_at}', to_jsonb($3::timestamptz), false)
 WHERE id=$1 AND status='failed'
 AND execution_info->'intelligence'->>'artwork'='missing' AND execution_info->'intelligence'->>'auto_verdict'='pass'
 AND (execution_info->'intelligence'->>'artwork_retry_at')::timestamptz<=$2`, id, due, next)
	return err
}
