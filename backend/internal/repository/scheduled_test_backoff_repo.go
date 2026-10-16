package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

func (r *scheduledTestResultRepository) GetTestBackoff(ctx context.Context, planID, accountID int64, key string) (*service.ScheduledTestBackoff, error) {
	state := &service.ScheduledTestBackoff{}
	err := r.db.QueryRowContext(ctx, `SELECT consecutive_failures,next_check_at FROM scheduled_test_account_backoffs WHERE plan_id=$1 AND account_id=$2 AND policy_key=$3`, planID, accountID, key).Scan(&state.ConsecutiveFailures, &state.NextCheckAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	return state, err
}
func (r *scheduledTestResultRepository) RecordTestBackoff(ctx context.Context, plan *service.ScheduledTestPlan, result *service.ScheduledTestResult, key string, failed bool, retryAfter *time.Time) error {
	if result.AccountID == nil {
		return nil
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Serialize with policy edits/new rounds. Stale completions cannot postpone a
	// newer run or resurrect an old configuration's cooldown.
	var current bool
	err = tx.QueryRowContext(ctx, `SELECT latest_run_id=$2 AND updated_at=$3 FROM scheduled_test_plans WHERE id=$1 FOR UPDATE`, plan.ID, result.RunID, plan.UpdatedAt).Scan(&current)
	if err != nil {
		return err
	}
	if !current {
		return nil
	}
	var count int
	var oldKey string
	var observed time.Time
	err = tx.QueryRowContext(ctx, `SELECT policy_key,consecutive_failures,observed_at FROM scheduled_test_account_backoffs WHERE plan_id=$1 AND account_id=$2 FOR UPDATE`, plan.ID, *result.AccountID).Scan(&oldKey, &count, &observed)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if observed.After(result.StartedAt) {
		return nil
	}
	if oldKey != key {
		count = 0
	}
	now := time.Now().UTC()
	next := now
	if failed {
		count++
		next = now.Add(service.ScheduledTestBackoffDelay(count))
		if retryAfter != nil && retryAfter.After(next) {
			next = *retryAfter
		}
	} else {
		count = 0
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_account_backoffs(plan_id,account_id,policy_key,consecutive_failures,next_check_at,observed_at) VALUES($1,$2,$3,$4,$5,$6)
 ON CONFLICT(plan_id,account_id) DO UPDATE SET policy_key=EXCLUDED.policy_key,consecutive_failures=EXCLUDED.consecutive_failures,next_check_at=EXCLUDED.next_check_at,observed_at=EXCLUDED.observed_at`, plan.ID, *result.AccountID, key, count, next, result.StartedAt)
	if err != nil {
		return err
	}
	metadata := map[string]any{"consecutive_failures": count, "next_check_at": nil}
	if failed {
		metadata["next_check_at"] = next
	}
	raw, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE scheduled_test_results SET execution_info=COALESCE(NULLIF(execution_info,'null'::jsonb),'{}'::jsonb)||$4::jsonb WHERE plan_id=$1 AND account_id=$2 AND run_id=$3`, plan.ID, *result.AccountID, result.RunID, raw)
	if err != nil {
		return err
	}
	return tx.Commit()
}
