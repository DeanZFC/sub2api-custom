package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// Policy writers hold the plan lock before taking account locks, matching
// result completion. A policy edit can refresh evidence or explicitly revoke
// its ownership, but cannot recover a held account using historical passes.
func reconcileScheduledTestPassRates(ctx context.Context, tx *sql.Tx, plan *service.ScheduledTestPlan, groupsChanged bool) error {
	rows, err := tx.QueryContext(ctx, `SELECT account_id,test_definition_id,model_key,blocked
 FROM scheduled_test_pass_rate_states WHERE plan_id=$1 ORDER BY account_id,test_definition_id,model_key`, plan.ID)
	if err != nil {
		return err
	}
	type ownedState struct {
		account, definition int64
		model               string
		blocked             bool
	}
	var states []ownedState
	for rows.Next() {
		var state ownedState
		if err := rows.Scan(&state.account, &state.definition, &state.model, &state.blocked); err != nil {
			_ = rows.Close()
			return err
		}
		states = append(states, state)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	rules := make(map[int64]service.ScheduledTestProtectionRule)
	if plan.Protection.Enabled {
		for _, rule := range plan.Protection.Rules {
			selected := slices.Contains(plan.TestDefinitionIDs, rule.TestDefinitionID) || (len(plan.TestDefinitionIDs) == 0 && plan.TestDefinitionID != nil && *plan.TestDefinitionID == rule.TestDefinitionID)
			if selected && rule.PassRate != nil && rule.PassRate.Enabled {
				rules[rule.TestDefinitionID] = rule
			}
		}
	}
	versions := make(map[int64]string)
	now := time.Now().UTC()
	var accountID int64
	accountInScope := false
	for _, state := range states {
		if accountID != state.account {
			accountID = state.account
			if _, err := lockProtectionAccount(ctx, tx, accountID); err != nil {
				return err
			}
			accountInScope = !slices.Contains(plan.ExcludedAccountIDs, accountID)
			// Automatic quality actions may have moved this account. Only an
			// explicit detection-scope edit should revoke that frozen ownership.
			if accountInScope && groupsChanged {
				if err := tx.QueryRowContext(ctx, `SELECT EXISTS (SELECT 1 FROM account_groups WHERE account_id=$1 AND group_id=ANY($2))`, accountID, pq.Array(plan.GroupIDs)).Scan(&accountInScope); err != nil {
					return err
				}
			}
		}
		rule, retained := rules[state.definition]
		if !retained || !accountInScope {
			// The delete trigger reprojects all other owners, including when a
			// plan or group is deleted by a foreign-key cascade.
			if _, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_pass_rate_states WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4`, plan.ID, state.account, state.definition, state.model); err != nil {
				return err
			}
			continue
		}
		version, ok := versions[state.definition]
		if !ok {
			version, err = scheduledTestPassRateVersion(ctx, tx, plan.ID, rule)
			if err != nil {
				return err
			}
			versions[state.definition] = version
		}
		config := rule.PassRate
		summary := service.ScheduledTestPassRateSummary{RuleVersion: version, Model: state.model, Blocked: state.blocked, WindowSize: config.WindowSize, WindowSeconds: config.WindowSeconds, PauseBelow: config.PauseBelow, RecoverAtLeast: config.RecoverAtLeast, RecoverConsecutive: config.RecoverConsecutive}
		if err := refreshScheduledTestPassRateSummary(ctx, tx, plan.ID, state.account, state.definition, state.model, &summary, now); err != nil {
			return err
		}
		raw, err := json.Marshal(summary)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_pass_rate_states SET rule_version=$5,summary=$6,updated_at=clock_timestamp() WHERE plan_id=$1 AND account_id=$2 AND test_definition_id=$3 AND model_key=$4`, plan.ID, state.account, state.definition, state.model, version, raw); err != nil {
			return err
		}
		if err := syncActivePassRateProjection(ctx, tx, state.account); err != nil {
			return err
		}
	}
	return nil
}

func lockScheduledTestLifecycleAccounts(ctx context.Context, tx *sql.Tx, planID int64) error {
	if _, err := lockProtectionPlan(ctx, tx, planID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	}
	// Legacy and pass-rate ownership can cover different account sets. Lock
	// their union once, before either reset path takes a subset of those locks.
	rows, err := tx.QueryContext(ctx, `SELECT account_id FROM scheduled_test_pass_rate_states WHERE plan_id=$1
 UNION SELECT account_id FROM scheduled_test_protection_states WHERE plan_id=$1
 UNION SELECT account_id FROM scheduled_test_combination_states WHERE plan_id=$1
 UNION SELECT account_id FROM scheduled_test_all_pass_rounds WHERE plan_id=$1 ORDER BY account_id`, planID)
	if err != nil {
		return err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	for _, id := range ids {
		if _, err := lockProtectionAccount(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}

func lockScheduledTestPassRateDefinitionPlans(ctx context.Context, tx *sql.Tx, definitionID int64) ([]*service.ScheduledTestPlan, error) {
	rows, err := tx.QueryContext(ctx, `SELECT p.id,p.protection,p.test_definition_ids,p.test_definition_id,p.group_ids,p.excluded_account_ids
 FROM scheduled_test_plans p WHERE p.test_definition_id=$1 OR $1=ANY(p.test_definition_ids)
 OR EXISTS (SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.plan_id=p.id AND s.test_definition_id=$1)
 ORDER BY p.id FOR NO KEY UPDATE`, definitionID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var plans []*service.ScheduledTestPlan
	for rows.Next() {
		plan := &service.ScheduledTestPlan{}
		var raw []byte
		if err := rows.Scan(&plan.ID, &raw, pq.Array(&plan.TestDefinitionIDs), &plan.TestDefinitionID, pq.Array(&plan.GroupIDs), pq.Array(&plan.ExcludedAccountIDs)); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &plan.Protection); err != nil {
			return nil, err
		}
		plans = append(plans, plan)
	}
	return plans, rows.Err()
}
