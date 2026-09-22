package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AccountCacheRecoveryGate = (*accountRepository)(nil)

// AcquireCacheRecoveryRequest is the final shared admission check after a
// concurrency reservation. It deliberately ignores cached account.extra: an
// expired snapshot must never extend a recovery trial or its request budget.
func (r *accountRepository) AcquireCacheRecoveryRequest(ctx context.Context, accountID int64) (bool, error) {
	if r == nil {
		return false, fmt.Errorf("cache recovery admission database is unavailable")
	}
	db, ok := r.sql.(interface {
		QueryRowContext(context.Context, string, ...any) *sql.Row
		BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
	})
	if !ok {
		return false, fmt.Errorf("cache recovery admission database is unavailable")
	}
	// Most accounts have no quality hold. Check live scheduling state and the
	// indexed hold set in one read without serializing ordinary traffic.
	var eligible, held bool
	err := db.QueryRowContext(ctx, `SELECT status='active' AND schedulable AND deleted_at IS NULL,
		EXISTS (SELECT 1 FROM scheduled_test_protection_states WHERE account_id=$1 AND blocked)
		FROM accounts WHERE id=$1`, accountID).Scan(&eligible, &held)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !eligible {
		return false, err
	}
	if !held {
		return true, nil
	}

	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	// Every protection writer takes this account lock before its state locks.
	// Gate calls never lock plans, preserving the plan -> account -> state order.
	err = tx.QueryRowContext(ctx, `SELECT status='active' AND schedulable AND deleted_at IS NULL
		FROM accounts WHERE id=$1 FOR NO KEY UPDATE`, accountID).Scan(&eligible)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil || !eligible {
		return false, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT recovery_phase,
		COALESCE(recovery_trial_started_at <= clock_timestamp() AND recovery_trial_ends_at > clock_timestamp(), FALSE),
		recovery_trial_requests,rule_config
		FROM scheduled_test_protection_states WHERE account_id=$1 AND blocked
		ORDER BY plan_id,test_definition_id FOR UPDATE`, accountID)
	if err != nil {
		return false, err
	}
	allowed := true
	for rows.Next() {
		var phase string
		var validWindow bool
		var used int64
		var raw []byte
		if err := rows.Scan(&phase, &validWindow, &used, &raw); err != nil {
			rows.Close()
			return false, err
		}
		var rule struct {
			Recovery struct {
				Enabled     bool  `json:"enabled"`
				MaxRequests int64 `json:"max_requests"`
			} `json:"recovery"`
		}
		if err := json.Unmarshal(raw, &rule); err != nil {
			rows.Close()
			return false, fmt.Errorf("decode cache recovery admission rule: %w", err)
		}
		if phase != "trial" || !validWindow || !rule.Recovery.Enabled || used < 0 || used >= rule.Recovery.MaxRequests {
			allowed = false
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil || !allowed {
		return false, err
	}
	// One request consumes a unit in every applicable trial. A stricter hold
	// therefore limits the entire account across plans, proxies and instances.
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_protection_states
		SET recovery_trial_requests=recovery_trial_requests+1
		WHERE account_id=$1 AND blocked`, accountID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
