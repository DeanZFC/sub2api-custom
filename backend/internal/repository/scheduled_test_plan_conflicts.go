package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Configuration changes serialize before plan -> account -> state locks. New
// conflicting plans cannot race one another; result writes never take this lock.
func lockScheduledTestPlanWrites(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006960,1735553655)`)
	return err
}
func enabledScheduledTestRoutingPlan(plan *service.ScheduledTestPlan) bool {
	return plan != nil && plan.Enabled && plan.Protection.Enabled && plan.HasGroupActions()
}
func scheduledTestRoutingScope(plan *service.ScheduledTestPlan) []int64 {
	ids := map[int64]bool{}
	for _, id := range plan.GroupIDs {
		ids[id] = true
	}
	for _, id := range plan.ProtectionActionGroupIDs() {
		ids[id] = true
	}
	return sortedProtectionIDs(ids)
}
func validateScheduledTestPlanConflicts(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan) error {
	if !enabledScheduledTestRoutingPlan(plan) {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,name,group_ids,protection FROM scheduled_test_plans WHERE enabled AND protection->>'enabled'='true' AND id<>$1 ORDER BY id`, plan.ID)
	if err != nil {
		return err
	}
	var others []*service.ScheduledTestPlan
	for rows.Next() {
		other := &service.ScheduledTestPlan{Enabled: true}
		var raw []byte
		if err := rows.Scan(&other.ID, &other.Name, pq.Array(&other.GroupIDs), &raw); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(raw, &other.Protection); err != nil {
			rows.Close()
			return err
		}
		if enabledScheduledTestRoutingPlan(other) {
			others = append(others, other)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	scope := scheduledTestRoutingScope(plan)
	for _, other := range others {
		var overlaps bool
		err := tx.QueryRowContext(ctx, `SELECT $1::bigint[] && $2::bigint[] OR EXISTS(
 SELECT 1 FROM accounts a WHERE a.deleted_at IS NULL
 AND EXISTS(SELECT 1 FROM account_groups x WHERE x.account_id=a.id AND x.group_id=ANY($1))
 AND EXISTS(SELECT 1 FROM account_groups y WHERE y.account_id=a.id AND y.group_id=ANY($2)))`, pq.Array(scope), pq.Array(scheduledTestRoutingScope(other))).Scan(&overlaps)
		if err != nil {
			return err
		}
		if overlaps {
			return fmt.Errorf("检测策略与已启用的策略「%s」(#%d)涉及相同分组或账号，请先停用冲突策略，避免重复检测和相互改组", other.Name, other.ID)
		}
	}
	return nil
}

// Recheck ownership on every frozen attempt and mutation: account edits can
// introduce overlaps after two otherwise disjoint strategies were configured.
// Caller SQL supplies the current plan alias p and account alias a.
const scheduledTestRuntimeOwnershipSQL = `(
 NOT (p.enabled AND p.protection->>'enabled'='true' AND EXISTS(
  SELECT 1 FROM jsonb_array_elements(COALESCE(CASE WHEN p.protection->>'mode'='combined'
   THEN p.protection->'combinations' ELSE p.protection->'rules' END,'[]'::jsonb)) rule
  WHERE CASE WHEN p.protection->>'mode'='combined' THEN rule->'action'->>'group_mode'='assign'
   ELSE rule->'on_pass'->>'group_mode'='assign' OR rule->'on_fail'->>'group_mode'='assign' END))
 OR NOT EXISTS(SELECT 1 FROM scheduled_test_plans other
  WHERE other.id<>p.id AND other.enabled AND other.protection->>'enabled'='true'
  AND EXISTS(SELECT 1 FROM jsonb_array_elements(COALESCE(CASE WHEN other.protection->>'mode'='combined'
   THEN other.protection->'combinations' ELSE other.protection->'rules' END,'[]'::jsonb)) rule
   WHERE CASE WHEN other.protection->>'mode'='combined' THEN rule->'action'->>'group_mode'='assign'
    ELSE rule->'on_pass'->>'group_mode'='assign' OR rule->'on_fail'->>'group_mode'='assign' END)
  AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(other.group_ids))))`

func scheduledTestAccountOwnershipCurrent(ctx context.Context, tx *sql.Tx, planID, accountID int64) (bool, error) {
	var current bool
	err := tx.QueryRowContext(ctx, `SELECT `+scheduledTestRuntimeOwnershipSQL+` FROM scheduled_test_plans p JOIN accounts a ON a.id=$2 WHERE p.id=$1`, planID, accountID).Scan(&current)
	return current, err
}
