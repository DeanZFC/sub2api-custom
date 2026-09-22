package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Serialize configuration changes before taking any plan/account/state locks.
// A row lock alone cannot exclude a concurrently created conflicting plan.
// Runtime result writes never acquire this lock; their plan -> account -> state
// order remains unchanged. Run timestamps do not change routing configuration.
func lockScheduledTestPlanWrites(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006960, 1735553655)`)
	return err
}

func enabledScheduledTestRoutingPlan(plan *service.ScheduledTestPlan) bool {
	return plan != nil && plan.Enabled && plan.Protection.Enabled &&
		(plan.Protection.GroupWorkflow != nil || plan.HasGroupActions())
}

// Ordinary action plans retain their existing coexistence policy. A dedicated
// workflow owns both tiers, so any overlapping action plan must be disabled
// explicitly before the workflow can be enabled (and vice versa).
func validateScheduledTestGroupWorkflowConflicts(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan) error {
	if !enabledScheduledTestRoutingPlan(plan) {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name,account_id,group_id,target_mode,protection
 FROM scheduled_test_plans WHERE enabled AND protection->>'enabled'='true' AND id<>$1 ORDER BY id`, plan.ID)
	if err != nil {
		return err
	}
	// Finish reading before querying membership through the same transaction.
	var others []*service.ScheduledTestPlan
	for rows.Next() {
		other := &service.ScheduledTestPlan{Enabled: true}
		var raw []byte
		if err := rows.Scan(&other.ID, &other.Name, &other.AccountID, &other.GroupID, &other.TargetMode, &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &other.Protection); err != nil {
			rows.Close()
			return err
		}
		if enabledScheduledTestRoutingPlan(other) &&
			(plan.Protection.GroupWorkflow != nil || other.Protection.GroupWorkflow != nil) {
			others = append(others, other)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	for _, other := range others {
		workflow, actionPlan := plan, other
		if workflow.Protection.GroupWorkflow == nil {
			workflow, actionPlan = other, plan
		}
		conflicts, err := scheduledTestWorkflowOverlapsPlan(ctx, tx, workflow, actionPlan)
		if err != nil {
			return err
		}
		if conflicts {
			return fmt.Errorf("分组工作流与已启用的检测计划「%s」(#%d)管理的分组或账号重叠，请先停用该计划，再启用当前计划，避免重复检测和相互改组", other.Name, other.ID)
		}
	}
	return nil
}

func scheduledTestWorkflowOverlapsPlan(ctx context.Context, tx *sql.Tx, workflow, other *service.ScheduledTestPlan) (bool, error) {
	c := workflow.Protection.GroupWorkflow
	groups := []int64{c.PassGroupID, c.FailGroupID}
	isWorkflowGroup := func(id int64) bool { return id == c.PassGroupID || id == c.FailGroupID }
	if other.GroupID != nil && isWorkflowGroup(*other.GroupID) {
		return true, nil
	}
	if other.Protection.GroupWorkflow != nil &&
		(isWorkflowGroup(other.Protection.GroupWorkflow.PassGroupID) || isWorkflowGroup(other.Protection.GroupWorkflow.FailGroupID)) {
		return true, nil
	}
	for _, rule := range other.Protection.Rules {
		for _, id := range rule.ManagedGroupIDs() {
			if isWorkflowGroup(id) {
				return true, nil
			}
		}
	}
	var otherSourceGroups []int64
	if other.GroupID != nil {
		otherSourceGroups = append(otherSourceGroups, *other.GroupID)
	}
	if other.Protection.GroupWorkflow != nil {
		otherSourceGroups = append(otherSourceGroups, other.Protection.GroupWorkflow.PassGroupID, other.Protection.GroupWorkflow.FailGroupID)
	}
	// Source groups can differ while sharing the same account. Account-only
	// plans and accounts retained for retesting after an earlier automatic move
	// also participate even when their source group is no longer a membership.
	var overlap bool
	err := tx.QueryRowContext(ctx, `SELECT EXISTS (
	 SELECT 1 FROM accounts a WHERE a.deleted_at IS NULL
	 AND (EXISTS (SELECT 1 FROM account_groups workflow_group WHERE workflow_group.account_id=a.id AND workflow_group.group_id=ANY($1::bigint[]))
	      OR EXISTS (SELECT 1 FROM scheduled_test_managed_accounts workflow_managed WHERE workflow_managed.plan_id=$5 AND workflow_managed.account_id=a.id AND workflow_managed.source_group_id=$6))
	 AND (($2::bigint IS NOT NULL AND a.id=$2)
	 OR ($2::bigint IS NULL AND (
	     EXISTS (SELECT 1 FROM account_groups source_group WHERE source_group.account_id=a.id AND source_group.group_id=ANY($3::bigint[]))
	     OR EXISTS (SELECT 1 FROM scheduled_test_managed_accounts ma WHERE ma.plan_id=$4 AND ma.account_id=a.id)
	 ))))`, pq.Array(groups), other.AccountID, pq.Array(otherSourceGroups), other.ID, workflow.ID, workflow.GroupID).Scan(&overlap)
	return overlap, err
}
