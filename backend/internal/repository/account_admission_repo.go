package repository

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AccountAdmissionGate = (*accountRepository)(nil)

// CheckAccountAdmission is the final shared admission check after a
// concurrency reservation. Active model protection is read from the database
// so a stale scheduler snapshot cannot bypass a capability hold.
func (r *accountRepository) CheckAccountAdmission(ctx context.Context, accountID int64) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("active test admission database is unavailable")
	}
	db, ok := r.sql.(interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
	})
	if !ok {
		return false, fmt.Errorf("active test admission database is unavailable")
	}
	model := service.ActivePassRateRequestModel(ctx)
	var eligible, held bool
	err := db.QueryRowContext(ctx, `SELECT status IN ('active','quality_paused') AND schedulable AND deleted_at IS NULL,
        status='quality_paused' OR EXISTS (SELECT 1 FROM scheduled_test_combination_states WHERE account_id=$1 AND blocked AND (account_wide OR $2='' OR model_key='' OR model_key=$2))
        OR COALESCE(extra->>'active_test_all_models_paused','false')='true'
        OR (COALESCE(extra->'active_test_model_holds','{}'::jsonb) ? $2 OR COALESCE(extra->'active_test_model_holds','{}'::jsonb) ? ''
          OR ($2='' AND COALESCE(extra->'active_test_model_holds','{}'::jsonb)<>'{}'::jsonb))
        OR EXISTS (SELECT 1 FROM scheduled_test_pass_rate_states WHERE account_id=$1 AND blocked AND ($2='' OR model_key='' OR model_key=$2))
        OR EXISTS (SELECT 1 FROM scheduled_test_protection_states WHERE account_id=$1 AND blocked AND ($2='' OR model_key='' OR model_key=$2))
		FROM accounts WHERE id=$1`, accountID, model).Scan(&eligible, &held)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !eligible {
		return false, err
	}
	if held {
		return false, service.ErrAccountQualityHeld
	}
	return true, nil
}
