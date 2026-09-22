package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
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

func (r *scheduledTestResultRepository) BeginRun(ctx context.Context, planID int64, runID string, inputs []*service.ScheduledTestResult) ([]*service.ScheduledTestResult, error) {
	if runID == "" || len(inputs) == 0 {
		return nil, fmt.Errorf("execution ID and target results are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var id int64
	if err = tx.QueryRowContext(ctx, `UPDATE scheduled_test_plans SET latest_run_id=$2 WHERE id=$1 RETURNING id`, planID, runID).Scan(&id); err != nil {
		return nil, err
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
