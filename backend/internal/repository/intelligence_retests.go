package repository

import (
	"context"
	"database/sql"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

// Membership and administrative account status are checked again immediately before each upstream request.
const intelligenceDueFrom = ` FROM intelligence_model_states s JOIN accounts a ON a.id=s.account_id
 WHERE s.plan_id=p.id AND s.next_check<=$1 AND s.model_key=ANY(p.model_ids)
 AND NOT(s.account_id=ANY(p.excluded_account_ids)) AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
 AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(p.group_ids))`

func (r *scheduledTestPlanRepository) DueIntelligenceTargets(ctx context.Context, id int64, at time.Time) ([]service.IntelligenceTarget, error) {
	return dueIntelligenceTargets(ctx, r.db, id, at)
}
func dueIntelligenceTargets(ctx context.Context, q qualityQuery, id int64, at time.Time) ([]service.IntelligenceTarget, error) {
	rows, err := q.QueryContext(ctx, `SELECT t.account_id,t.model_key FROM scheduled_test_plans p CROSS JOIN LATERAL (SELECT s.account_id,s.model_key`+intelligenceDueFrom+`) t WHERE p.id=$2 AND p.enabled ORDER BY 1,2`, at, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	targets := []service.IntelligenceTarget{}
	for rows.Next() {
		var t service.IntelligenceTarget
		if err = rows.Scan(&t.AccountID, &t.Model); err != nil {
			return nil, err
		}
		targets = append(targets, t)
	}
	return targets, rows.Err()
}

// A lost worker leaves a frozen target with no usable outcome. Record that
// interruption before closing its lease so recovery cannot bridge the gap.
func recordAbandonedIntelligence(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan) error {
	rows, err := tx.QueryContext(ctx, `WITH interrupted AS (
 INSERT INTO intelligence_observations(result_id,plan_id,account_id,model_key,policy_version,run_id,started_at,finished_at,verdict,auto_isolate,recovery_passes,cooldown_minutes)
 SELECT r.id,p.id,r.account_id,r.model_id,r.execution_info->'intelligence'->>'policy_version',r.run_id,r.started_at,clock_timestamp(),'unknown',false,
 (p.protection->'intelligence'->>'recovery_passes')::int,(p.protection->'intelligence'->>'cooldown_minutes')::int
 FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id JOIN accounts a ON a.id=r.account_id
 WHERE p.id=$1 AND p.enabled AND p.protection->'intelligence'->>'version'=r.execution_info->'intelligence'->>'policy_version'
 AND r.status IN ('pending','running') AND NOT r.protection_revoked AND r.run_id<>'' AND r.model_id=ANY(p.model_ids)
 AND a.schedulable AND a.deleted_at IS NULL AND a.status IN ('active','quality_paused') AND NOT(a.id=ANY(p.excluded_account_ids))
 AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(p.group_ids))
 AND EXISTS(SELECT 1 FROM scheduled_test_execution_leases l WHERE l.plan_id=p.id AND l.scope='plan' AND l.expires_at<=clock_timestamp())
 ON CONFLICT DO NOTHING RETURNING account_id,model_key)
 SELECT DISTINCT account_id,model_key FROM interrupted ORDER BY account_id,model_key`, plan.ID)
	if err != nil {
		return err
	}
	var targets []service.IntelligenceTarget
	for rows.Next() {
		var target service.IntelligenceTarget
		if err = rows.Scan(&target.AccountID, &target.Model); err != nil {
			_ = rows.Close()
			return err
		}
		targets = append(targets, target)
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	for _, t := range targets {
		if _, err = lockProtectionAccount(ctx, tx, t.AccountID); err != nil {
			return err
		}
		if err = updateIntelligenceState(ctx, tx, plan.ID, t.AccountID, t.Model, nil); err != nil {
			return err
		}
	}
	return nil
}
