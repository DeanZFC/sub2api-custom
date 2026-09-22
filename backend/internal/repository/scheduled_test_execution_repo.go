package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.ScheduledTestRunRepository = (*scheduledTestResultRepository)(nil)

func (r *scheduledTestResultRepository) RecordDecision(ctx context.Context, result *service.ScheduledTestResult, decision service.ScheduledTestProtectionDecision) error {
	raw, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	_, err = r.db.ExecContext(ctx, `UPDATE scheduled_test_results SET protection_decision=$2::jsonb WHERE id=$1 AND started_at=$3`, result.ID, raw, result.StartedAt.Round(time.Microsecond))
	return err
}

func (r *scheduledTestResultRepository) BeginRun(ctx context.Context, plan *service.ScheduledTestPlan, runID string, inputs []*service.ScheduledTestResult) ([]*service.ScheduledTestResult, error) {
	if plan == nil || runID == "" || len(inputs) == 0 {
		return nil, fmt.Errorf("execution ID and target results are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if err := lockScheduledTestPlanWrites(ctx, tx); err != nil {
		return nil, err
	}
	current, err := scanPlan(tx.QueryRowContext(ctx, `SELECT id, name, sort_order, account_id, group_id, test_definition_id, test_type, target_mode, model_id, reasoning_effort, cron_expression, enabled, max_results, auto_recover, last_run_at, next_run_at, created_at, updated_at, test_definition_ids, protection, group_ids, migration_note FROM scheduled_test_plans WHERE id=$1 FOR NO KEY UPDATE`, plan.ID))
	if err != nil {
		return nil, err
	}
	if !scheduledTestSameExecutionPolicy(current, plan) {
		return nil, fmt.Errorf("test policy changed before execution")
	}
	if err := validateScheduledTestPlanConflicts(ctx, tx, current); err != nil {
		return nil, err
	}
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_plans SET latest_run_id=$2 WHERE id=$1`, plan.ID, runID); err != nil {
		return nil, err
	}
	planID := plan.ID
	accountIDs := map[int64]bool{}
	for _, input := range inputs {
		if input != nil && input.AccountID != nil {
			accountIDs[*input.AccountID] = true
		}
	}
	for _, accountID := range sortedProtectionIDs(accountIDs) {
		if _, err := lockProtectionAccount(ctx, tx, accountID); err != nil {
			return nil, err
		}
		owned, err := scheduledTestAccountOwnershipCurrent(ctx, tx, planID, accountID)
		if err != nil {
			return nil, err
		}
		if !owned {
			return nil, fmt.Errorf("account belongs to conflicting enabled test strategies")
		}
	}
	results := make([]*service.ScheduledTestResult, 0, len(inputs))
	for _, input := range inputs {
		if input == nil || input.PlanID != planID {
			return nil, fmt.Errorf("execution target belongs to a different plan")
		}
		input.RunID = runID
		result, err := createScheduledTestResult(ctx, tx, input)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return results, nil
}

// Display order and public ballot visibility do not alter a running strategy.
// Every change to execution or routing semantics invalidates its frozen round.
func scheduledTestSameExecutionPolicy(a, b *service.ScheduledTestPlan) bool {
	if a == nil || b == nil {
		return false
	}
	ag, bg := slices.Clone(a.GroupIDs), slices.Clone(b.GroupIDs)
	slices.Sort(ag)
	slices.Sort(bg)
	return slices.Equal(ag, bg) && reflect.DeepEqual(a.AccountID, b.AccountID) &&
		reflect.DeepEqual(a.TestDefinitionIDs, b.TestDefinitionIDs) && reflect.DeepEqual(a.TestDefinitionID, b.TestDefinitionID) &&
		a.TargetMode == b.TargetMode && a.TestType == b.TestType && a.ModelID == b.ModelID && a.ReasoningEffort == b.ReasoningEffort &&
		a.Enabled == b.Enabled && a.AutoRecover == b.AutoRecover && protectionConfigSamePolicy(a.Protection, b.Protection)
}

type protectionAccountSnapshot struct {
	status string
	groups map[int64]bool
}

func readProtectionAccountSnapshot(ctx context.Context, tx *sql.Tx, accountID int64) (*protectionAccountSnapshot, error) {
	snapshot := &protectionAccountSnapshot{groups: make(map[int64]bool)}
	if err := tx.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=$1`, accountID).Scan(&snapshot.status); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT group_id FROM account_groups WHERE account_id=$1`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		snapshot.groups[id] = true
	}
	return snapshot, rows.Err()
}

func recordProtectionAction(ctx context.Context, tx *sql.Tx, state *protectionState, before *protectionAccountSnapshot, verdict, reason string) error {
	after, err := readProtectionAccountSnapshot(ctx, tx, state.accountID)
	if err != nil {
		return err
	}
	decision := service.ScheduledTestProtectionDecision{Status: "unchanged", Verdict: verdict, Reason: reason, Scheduling: "keep"}
	if verdict == "pending" {
		decision.Status = "pending"
	}
	if before.status != after.status {
		if after.status == "quality_paused" {
			decision.Scheduling = "pause"
		} else if before.status == "quality_paused" && after.status == "active" {
			decision.Scheduling = "resume"
		}
	}
	for _, id := range sortedProtectionIDs(after.groups) {
		if !before.groups[id] {
			decision.AddedGroupIDs = append(decision.AddedGroupIDs, id)
		}
	}
	for _, id := range sortedProtectionIDs(before.groups) {
		if !after.groups[id] {
			decision.RemovedGroupIDs = append(decision.RemovedGroupIDs, id)
		}
	}
	if decision.Scheduling != "keep" || len(decision.AddedGroupIDs)+len(decision.RemovedGroupIDs) > 0 {
		decision.Status = "applied"
		decision.GroupIDs = sortedProtectionIDs(after.groups)
	}
	raw, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	// Repeated votes that do not change the verdict or account must not erase
	// the action that this result already applied.
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_results SET protection_decision=
	 CASE WHEN $2::jsonb->>'status'='unchanged' AND protection_decision->>'status'='applied'
	 AND protection_decision->>'verdict'=$2::jsonb->>'verdict' THEN protection_decision ELSE $2::jsonb END
	 WHERE id=$1 AND started_at=$3`, state.resultID, raw, state.started)
	return err
}
