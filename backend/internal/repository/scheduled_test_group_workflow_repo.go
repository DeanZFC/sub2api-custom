package repository

import (
	"context"
	"database/sql"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Both tiers belong to one recurring workflow. Previously enrolled accounts
// remain targets even if a manual group edit temporarily removes both tiers.
func (r *scheduledTestResultRepository) listGroupWorkflowAccountIDs(ctx context.Context, plan *service.ScheduledTestPlan, accountID *int64, eligibleOnly bool) ([]int64, error) {
	workflow := plan.Protection.GroupWorkflow
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
 WHERE a.deleted_at IS NULL AND ($1::bigint IS NULL OR a.id=$1)
 AND (NOT $4 OR (a.schedulable AND a.status IN ('active','quality_paused')))
 AND (EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY($2))
      OR EXISTS (SELECT 1 FROM scheduled_test_managed_accounts ma WHERE ma.plan_id=$3 AND ma.account_id=a.id AND ma.source_group_id=$5))
 ORDER BY a.id`, accountID, pq.Array([]int64{workflow.PassGroupID, workflow.FailGroupID}), plan.ID, eligibleOnly, plan.GroupID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	ids := make([]int64, 0)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

// The caller holds the plan, account and triggering state locks. A manual
// review in this round overrides the numeric result until the next full round;
// merely producing an animation never moves an account.
func reconcileGroupWorkflow(ctx context.Context, tx *sql.Tx, state *protectionState, config *service.ScheduledTestProtectionConfig, verdict string) (manualOverride bool, err error) {
	workflow := config.GroupWorkflow
	if workflow == nil || (verdict != "pass" && verdict != "fail") {
		return manualOverride, nil
	}
	switch state.definitionID {
	case workflow.ReviewTestID:
		if state.adminVerdict != "pass" && state.adminVerdict != "fail" {
			return manualOverride, nil
		}
		verdict = state.adminVerdict
	case workflow.AutomaticTestID:
		var reviewVerdict string
		err := tx.QueryRowContext(ctx, `SELECT s.admin_verdict
 FROM scheduled_test_protection_states s JOIN scheduled_test_results r ON r.id=s.result_id
 WHERE s.plan_id=$1 AND s.account_id=$2 AND s.test_definition_id=$3
 AND s.round_started_at=$4 AND s.completed AND s.admin_verdict IN ('pass','fail')
 AND r.started_at=s.result_started_at AND r.started_at>=s.round_started_at
 AND r.status IN ('success','passed')`, state.planID, state.accountID, workflow.ReviewTestID, state.roundStarted).Scan(&reviewVerdict)
		if err != nil && err != sql.ErrNoRows {
			return manualOverride, err
		}
		if err == nil {
			manualOverride = true
			verdict = reviewVerdict
		}
	default:
		return manualOverride, nil
	}
	target := workflow.FailGroupID
	if verdict == "pass" {
		target = workflow.PassGroupID
	}
	before, err := readProtectionAccountSnapshot(ctx, tx, state.accountID)
	if err != nil {
		return manualOverride, err
	}
	if len(before.groups) == 1 && before.groups[target] {
		return manualOverride, nil
	}
	affected := map[int64]bool{workflow.PassGroupID: true, workflow.FailGroupID: true}
	for id := range before.groups {
		affected[id] = true
	}
	// Lock every old/new group together in ID order, including soft-deleted old
	// memberships that this explicit replacement is allowed to remove.
	rows, err := tx.QueryContext(ctx, `SELECT id FROM groups WHERE id=ANY($1) ORDER BY id FOR SHARE`, pq.Array(sortedProtectionIDs(affected)))
	if err != nil {
		return manualOverride, err
	}
	for rows.Next() {
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return manualOverride, err
	}
	var compatible int
	err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups g JOIN accounts a ON a.id=$1
 WHERE g.id=ANY($2) AND g.deleted_at IS NULL AND g.status='active'
 AND g.platform=a.platform AND g.platform<>'composite'`, state.accountID, pq.Array([]int64{workflow.PassGroupID, workflow.FailGroupID})).Scan(&compatible)
	if err != nil {
		return manualOverride, err
	}
	if compatible != 2 {
		return manualOverride, fmt.Errorf("quality workflow groups are unavailable or have incompatible platforms")
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND group_id<>$2`, state.accountID, target); err != nil {
		return manualOverride, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,created_at)
 VALUES($1,$2,50,NOW()) ON CONFLICT(account_id,group_id) DO NOTHING`, state.accountID, target); err != nil {
		return manualOverride, err
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_test_managed_accounts(plan_id,account_id,source_group_id)
 SELECT id,$2,group_id FROM scheduled_test_plans WHERE id=$1 AND group_id IS NOT NULL
 ON CONFLICT(plan_id,account_id) DO NOTHING`, state.planID, state.accountID); err != nil {
		return manualOverride, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET updated_at=NOW() WHERE id=$1`, state.accountID); err != nil {
		return manualOverride, err
	}
	return manualOverride, enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &state.accountID, nil, buildSchedulerGroupPayload(sortedProtectionIDs(affected)))
}
