package repository

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestLeaseRepository = (*scheduledTestPlanRepository)(nil)

// Takeover and result writes lock the plan in the same order. An expired
// worker cannot race a replacement while committing a result or retry.
func execScheduledTestResultUnderLease(ctx context.Context, db *sql.DB, result *service.ScheduledTestResult, query string, args ...any) (sql.Result, error) {
	if result.ExecutionLeaseOwner == "" {
		return db.ExecContext(ctx, query, args...)
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	var id int64
	if err := tx.QueryRowContext(ctx, "SELECT id FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE", result.PlanID).Scan(&id); err != nil {
		return nil, err
	}
	if err := checkScheduledTestResultLease(ctx, tx, result); err != nil {
		return nil, err
	}
	changed, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return changed, nil
}

func checkScheduledTestResultLease(ctx context.Context, tx *sql.Tx, result *service.ScheduledTestResult) error {
	if result.ExecutionLeaseOwner == "" {
		return nil
	}
	var valid bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_test_execution_leases WHERE plan_id=$1 AND scope=$2 AND owner=$3 AND expires_at>clock_timestamp())`, result.PlanID, result.ExecutionLeaseScope, result.ExecutionLeaseOwner).Scan(&valid)
	if err != nil {
		return err
	}
	if !valid {
		return fmt.Errorf("test execution lease lost")
	}
	return nil
}

func (r *scheduledTestPlanRepository) ClaimExecution(ctx context.Context, plan *service.ScheduledTestPlan, scope, owner string, automatic bool) (bool, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	// Serializes competing full-round and retry claims without holding a
	// transaction or dedicated connection across paid upstream requests.
	var enabled bool
	var next sql.NullTime
	var now time.Time
	if err = tx.QueryRowContext(ctx, `SELECT enabled,next_run_at,clock_timestamp() FROM scheduled_test_plans WHERE id=$1 FOR UPDATE`, plan.ID).Scan(&enabled, &next, &now); err != nil {
		return false, err
	}
	if automatic && (!enabled || !next.Valid || next.Time.After(now) || plan.NextRunAt == nil || !next.Time.Equal(plan.NextRunAt.Round(time.Microsecond))) {
		return false, nil
	}
	// Close abandoned work without treating it as quality evidence or replaying
	// the original paid request. A later round starts with a fresh snapshot.
	if _, err = tx.ExecContext(ctx, "UPDATE scheduled_test_results r SET status='failed',error_message='检测中断：执行租约过期，上游结果未知',finished_at=clock_timestamp() WHERE r.plan_id=$1 AND r.status IN ('pending','running') AND EXISTS(SELECT 1 FROM scheduled_test_execution_leases l WHERE l.plan_id=r.plan_id AND l.expires_at<=clock_timestamp() AND (l.scope='plan' OR l.scope='retry:' || r.account_id::text || ':' || COALESCE(r.test_definition_id,0)::text || ':' || r.model_id))", plan.ID); err != nil {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM scheduled_test_execution_leases WHERE plan_id=$1 AND expires_at<=clock_timestamp()`, plan.ID); err != nil {
		return false, err
	}
	var conflict bool
	if err = tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM scheduled_test_execution_leases WHERE plan_id=$1 AND (scope='plan' OR $2='plan' OR scope=$2))`, plan.ID, scope).Scan(&conflict); err != nil || conflict {
		return false, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_execution_leases(plan_id,scope,owner,expires_at) VALUES($1,$2,$3,clock_timestamp()+$4*interval '1 second')`, plan.ID, scope, owner, int(service.ScheduledTestLeaseTTL.Seconds())); err != nil {
		return false, err
	}
	if err = tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *scheduledTestPlanRepository) RenewExecution(ctx context.Context, planID int64, scope, owner string) (bool, error) {
	result, err := r.db.ExecContext(ctx, `UPDATE scheduled_test_execution_leases SET expires_at=clock_timestamp()+$4*interval '1 second' WHERE plan_id=$1 AND scope=$2 AND owner=$3 AND expires_at>clock_timestamp()`, planID, scope, owner, int(service.ScheduledTestLeaseTTL.Seconds()))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n == 1, err
}

func (r *scheduledTestPlanRepository) ReleaseExecution(ctx context.Context, planID int64, scope, owner string) error {
	_, err := r.db.ExecContext(ctx, `DELETE FROM scheduled_test_execution_leases WHERE plan_id=$1 AND scope=$2 AND owner=$3`, planID, scope, owner)
	return err
}
