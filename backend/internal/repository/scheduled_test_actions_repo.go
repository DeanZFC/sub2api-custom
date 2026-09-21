package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.ScheduledTestActionRepository = (*scheduledTestResultRepository)(nil)
var _ service.ScheduledTestActionRoundRepository = (*scheduledTestResultRepository)(nil)

// The source group remains the visibility/authorization boundary for a test.
// Only an automatic move can enroll an account for subsequent off-group retests.
const protectionSourceMembershipSQL = `(r.group_id IS NULL
 OR EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=r.account_id AND ag.group_id=r.group_id)
 OR EXISTS (SELECT 1 FROM scheduled_test_managed_accounts ma
            WHERE ma.plan_id=r.plan_id AND ma.account_id=r.account_id AND ma.source_group_id=r.group_id))`

func (r *scheduledTestResultRepository) ListPlanDetectionAccountIDs(ctx context.Context, plan *service.ScheduledTestPlan, accountID *int64) ([]int64, error) {
	if plan == nil {
		return nil, fmt.Errorf("test plan is required")
	}
	if !plan.HasGroupActions() {
		return r.ListDetectionAccountIDs(ctx, plan.GroupID, accountID)
	}
	rows, err := r.db.QueryContext(ctx, `SELECT a.id FROM accounts a
 WHERE a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
 AND ($1::bigint IS NULL OR a.id=$1)
 AND ($2::bigint IS NULL OR EXISTS (SELECT 1 FROM groups g WHERE g.id=$2 AND g.deleted_at IS NULL)
 AND (EXISTS (SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=$2)
      OR EXISTS (SELECT 1 FROM scheduled_test_managed_accounts ma JOIN scheduled_test_plans p ON p.id=ma.plan_id
                 WHERE ma.plan_id=$3 AND ma.account_id=a.id AND ma.source_group_id=$2
                 AND p.enabled AND p.protection->>'enabled'='true' AND p.group_id=$2)))
 ORDER BY a.id`, accountID, plan.GroupID, plan.ID)
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

func (r *scheduledTestPlanRepository) validateProtectionGroups(ctx context.Context, plan *service.ScheduledTestPlan) error {
	if !plan.HasGroupActions() {
		return nil
	}
	ids := make(map[int64]bool)
	for _, rule := range plan.Protection.Rules {
		for _, id := range rule.ManagedGroupIDs() {
			ids[id] = true
		}
	}
	var platform string
	if plan.GroupID != nil {
		if err := r.db.QueryRowContext(ctx, `SELECT platform FROM groups WHERE id=$1 AND deleted_at IS NULL`, *plan.GroupID).Scan(&platform); err != nil {
			return err
		}
	} else if plan.AccountID != nil {
		if err := r.db.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id=$1 AND deleted_at IS NULL`, *plan.AccountID).Scan(&platform); err != nil {
			return err
		}
	}
	// Composite groups do not directly own scheduler account pools.
	if platform == "" || platform == "composite" {
		return fmt.Errorf("quality group actions require a concrete source platform")
	}
	rows, err := r.db.QueryContext(ctx, `SELECT id,platform,status FROM groups WHERE id=ANY($1) AND deleted_at IS NULL`, pq.Array(sortedProtectionIDs(ids)))
	if err != nil {
		return err
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var id int64
		var targetPlatform, status string
		if err := rows.Scan(&id, &targetPlatform, &status); err != nil {
			return err
		}
		if targetPlatform != platform || status != "active" {
			return fmt.Errorf("quality target group %d must be active and use source platform %s", id, platform)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		return err
	}
	if count != len(ids) {
		return fmt.Errorf("quality target group no longer exists")
	}
	return nil
}

// Enroll all types before the first type finishes, so a quick statistics result
// cannot upgrade an account while its candy/pelican result has never been judged.
func initializeProtectionActionStates(ctx context.Context, tx *sql.Tx, planID, accountID int64, config *service.ScheduledTestProtectionConfig) error {
	for _, rule := range config.Rules {
		raw, err := json.Marshal(rule)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_protection_states
   (plan_id,account_id,test_definition_id,result_started_at,rule_config)
   VALUES ($1,$2,$3,'epoch',$4::jsonb) ON CONFLICT (plan_id,account_id,test_definition_id) DO NOTHING`, planID, accountID, rule.TestDefinitionID, raw); err != nil {
			return err
		}
	}
	return nil
}

type protectionRoutingRule struct {
	currentVerdict string
	planID         int64
	sourceGroupID  sql.NullInt64
	rule           service.ScheduledTestProtectionRule
	verdict        string
}

type protectionRoutingChange struct {
	scope, desired map[int64]bool
	rules          []protectionRoutingRule
}

// Connected rules share a tier: failures win, while promotion requires every
// participating rule to have a conclusive pass. Disjoint tier sets are independent.
// Pending rounds retain their previous routing_verdict in the database.
func protectionRoutingChanges(rules []protectionRoutingRule, current map[int64]bool) []protectionRoutingChange {
	visited := make([]bool, len(rules))
	changes := make([]protectionRoutingChange, 0)
	for i := range rules {
		if visited[i] {
			continue
		}
		scope := make(map[int64]bool)
		for _, id := range rules[i].rule.ManagedGroupIDs() {
			scope[id] = true
		}
		if len(scope) == 0 {
			visited[i] = true
			continue
		}
		component := []int{i}
		visited[i] = true
		for expanded := true; expanded; {
			expanded = false
			for j := range rules {
				if visited[j] {
					continue
				}
				intersects := false
				for _, id := range rules[j].rule.ManagedGroupIDs() {
					if scope[id] {
						intersects = true
						break
					}
				}
				if !intersects {
					continue
				}
				visited[j] = true
				component = append(component, j)
				expanded = true
				for _, id := range rules[j].rule.ManagedGroupIDs() {
					scope[id] = true
				}
			}
		}
		failed, unknown := false, false
		for _, j := range component {
			failed = failed || rules[j].verdict == "fail"
			unknown = unknown || rules[j].currentVerdict != "pass"
		}
		if !failed && unknown {
			continue
		}
		desired := make(map[int64]bool)
		assign := false
		change := protectionRoutingChange{scope: scope, desired: desired}
		for _, j := range component {
			entry := rules[j]
			change.rules = append(change.rules, entry)
			if failed && entry.verdict != "fail" {
				continue
			}
			action := entry.rule.OutcomeAction(entry.verdict)
			if action.GroupMode == "assign" {
				assign = true
				for _, id := range action.GroupIDs {
					desired[id] = true
				}
			} else {
				for _, id := range entry.rule.ManagedGroupIDs() {
					if current[id] {
						desired[id] = true
					}
				}
			}
		}
		if assign {
			changes = append(changes, change)
		}
	}
	return changes
}

func sortedProtectionIDs(ids map[int64]bool) []int64 {
	result := make([]int64, 0, len(ids))
	for id := range ids {
		result = append(result, id)
	}
	sort.Slice(result, func(i, j int) bool { return result[i] < result[j] })
	return result
}

func reconcileProtectionGroups(ctx context.Context, tx *sql.Tx, accountID int64) error {
	rows, err := tx.QueryContext(ctx, `SELECT s.plan_id,p.group_id,s.rule_config,s.routing_verdict,p.protection,s.verdict
 FROM scheduled_test_protection_states s JOIN scheduled_test_plans p ON p.id=s.plan_id
 WHERE s.account_id=$1 AND p.enabled AND p.protection->>'enabled'='true'
 ORDER BY s.plan_id,s.test_definition_id`, accountID)
	if err != nil {
		return err
	}
	rules := make([]protectionRoutingRule, 0)
	for rows.Next() {
		var entry protectionRoutingRule
		var raw, configRaw []byte
		if err := rows.Scan(&entry.planID, &entry.sourceGroupID, &raw, &entry.verdict, &configRaw, &entry.currentVerdict); err != nil {
			rows.Close()
			return err
		}
		var config service.ScheduledTestProtectionConfig
		if err := json.Unmarshal(raw, &entry.rule); err != nil {
			rows.Close()
			return err
		}
		if err := json.Unmarshal(configRaw, &config); err != nil {
			rows.Close()
			return err
		}
		if protectionRuleCurrent(&config, entry.rule) && len(entry.rule.ManagedGroupIDs()) > 0 {
			rules = append(rules, entry)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(rules) == 0 {
		return err
	}
	rows, err = tx.QueryContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1 ORDER BY group_id`, accountID)
	if err != nil {
		return err
	}
	current := make(map[int64]bool)
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		current[id] = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return err
	}
	changes := protectionRoutingChanges(rules, current)
	if len(changes) == 0 {
		return nil
	}
	// Acquire every affected group lock in ascending order before touching join
	// rows. This also follows the group-deletion lock protocol.
	scope := make(map[int64]bool)
	for _, change := range changes {
		for id := range change.scope {
			scope[id] = true
		}
	}
	ids := sortedProtectionIDs(scope)
	if err := lockLiveGroups(ctx, tx, ids); err != nil {
		return err
	}
	// Recheck source/target platforms inside the mutation transaction. Never make
	// a partial move after a configured group has been disabled or repurposed.
	for _, change := range changes {
		for _, entry := range change.rules {
			var platform string
			if entry.sourceGroupID.Valid {
				err = tx.QueryRowContext(ctx, `SELECT platform FROM groups WHERE id=$1 AND deleted_at IS NULL`, entry.sourceGroupID.Int64).Scan(&platform)
			} else {
				err = tx.QueryRowContext(ctx, `SELECT platform FROM accounts WHERE id=$1`, accountID).Scan(&platform)
			}
			if err != nil {
				return err
			}
			var count int
			err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups WHERE id=ANY($1) AND deleted_at IS NULL AND status='active' AND platform=$2 AND platform<>'composite'`, pq.Array(entry.rule.ManagedGroupIDs()), platform).Scan(&count)
			if err != nil {
				return err
			}
			if count != len(entry.rule.ManagedGroupIDs()) {
				return fmt.Errorf("quality target groups are unavailable or have incompatible platforms")
			}
		}
	}
	affected := make(map[int64]bool)
	changedPlans := make(map[int64]sql.NullInt64)
	for _, change := range changes {
		changed := false
		for _, id := range sortedProtectionIDs(change.scope) {
			if change.desired[id] {
				res, err := tx.ExecContext(ctx, `INSERT INTO account_groups(account_id,group_id,priority,created_at)
      VALUES ($1,$2,50,NOW()) ON CONFLICT (account_id,group_id) DO NOTHING`, accountID, id)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				if n > 0 {
					changed = true
					affected[id] = true
				}
			} else {
				res, err := tx.ExecContext(ctx, `DELETE FROM account_groups WHERE account_id=$1 AND group_id=$2`, accountID, id)
				if err != nil {
					return err
				}
				n, err := res.RowsAffected()
				if err != nil {
					return err
				}
				if n > 0 {
					changed = true
					affected[id] = true
				}
			}
		}
		if changed {
			for _, entry := range change.rules {
				changedPlans[entry.planID] = entry.sourceGroupID
			}
		}
	}
	if len(affected) == 0 {
		return nil
	}
	// Publish both removed and new groups so no old scheduler bucket retains this
	// account. Unrelated bindings and their priorities have never been touched.
	for id := range current {
		affected[id] = true
	}
	for _, change := range changes {
		for id := range change.desired {
			affected[id] = true
		}
	}
	for planID, source := range changedPlans {
		if !source.Valid {
			continue
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO scheduled_test_managed_accounts(plan_id,account_id,source_group_id)
   VALUES ($1,$2,$3) ON CONFLICT (plan_id,account_id) DO NOTHING`, planID, accountID, source.Int64); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx, `UPDATE accounts SET updated_at=NOW() WHERE id=$1`, accountID); err != nil {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountGroupsChanged, &accountID, nil, buildSchedulerGroupPayload(sortedProtectionIDs(affected)))
}

// Start a complete plan round before any type runs. Old judgments continue to
// hold existing tiers, but cannot authorize a fresh promotion while this round
// still has an untested type or outstanding votes.
func (r *scheduledTestResultRepository) BeginProtectionRun(ctx context.Context, plan *service.ScheduledTestPlan, started time.Time) error {
	if plan == nil || !plan.Protection.Enabled {
		return nil
	}
	ids, err := r.ListPlanDetectionAccountIDs(ctx, plan, plan.AccountID)
	if err != nil {
		return err
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	config, err := lockProtectionPlan(ctx, tx, plan.ID)
	if err != nil {
		return err
	}
	if !config.Enabled || !reflect.DeepEqual(*config, plan.Protection) {
		return fmt.Errorf("quality plan changed before execution")
	}
	for _, id := range ids {
		eligible, err := lockProtectionAccount(ctx, tx, id)
		if err != nil {
			return err
		}
		if !eligible {
			continue
		}
		if err := initializeProtectionActionStates(ctx, tx, plan.ID, id, config); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states SET
   round_started_at=$3,automated_verdict='pending',verdict='pending',completed=FALSE,generation=generation+1,
   admin_verdict='',admin_user_id=NULL,admin_decided_at=NULL,updated_at=NOW()
   WHERE plan_id=$1 AND account_id=$2 AND round_started_at<$3`, plan.ID, id, started.Round(time.Microsecond)); err != nil {
			return err
		}
	}
	return tx.Commit()
}
