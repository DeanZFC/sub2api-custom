package repository

import (
	"context"

	"github.com/lib/pq"
)

// Manual membership edits serialize with run creation and policy changes, then
// lock the affected plans before the account, like all protection writers.
func lockAccountMembershipProtection(ctx context.Context, tx sqlExecutor, accountID int64) ([]int64, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006960,1735553655)`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id FROM scheduled_test_plans p
 WHERE EXISTS (SELECT 1 FROM scheduled_test_protection_states s WHERE s.plan_id=p.id AND s.account_id=$1)
 OR EXISTS (SELECT 1 FROM scheduled_test_combination_states s WHERE s.plan_id=p.id AND s.account_id=$1)
 OR EXISTS (SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.plan_id=p.id AND s.account_id=$1)
 OR EXISTS (SELECT 1 FROM scheduled_test_results r WHERE r.plan_id=p.id AND r.account_id=$1
            AND r.run_id=p.latest_run_id AND r.run_id<>'' AND NOT r.protection_revoked)
 ORDER BY p.id FOR NO KEY UPDATE`, accountID)
	if err != nil {
		return nil, err
	}
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var id int64
	if err := scanSingleRow(ctx, tx, `SELECT id FROM accounts WHERE id=$1 AND deleted_at IS NULL FOR NO KEY UPDATE`, []any{accountID}, &id); err != nil {
		return nil, err
	}
	return ids, nil
}

// Only administrator membership edits call this helper. Automatic routing
// retains its frozen round, including moves made by another rule in that round.
func releaseDetachedAccountProtection(ctx context.Context, tx sqlExecutor, accountID int64, planIDs []int64) error {
	if err := releaseDetachedPassiveProtection(ctx, tx, accountID); err != nil {
		return err
	}
	if len(planIDs) == 0 {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id FROM scheduled_test_plans p
 WHERE p.id=ANY($2) AND NOT EXISTS (SELECT 1 FROM account_groups ag
 WHERE ag.account_id=$1 AND ag.group_id=ANY(p.group_ids)) ORDER BY p.id`, accountID, pq.Array(planIDs))
	if err != nil {
		return err
	}
	var detached []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return err
		}
		detached = append(detached, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil || len(detached) == 0 {
		return err
	}
	// Preserve history and run IDs. Revocation survives late result updates and
	// moving the account back; only a newly frozen round may act again.
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_results r SET protection_revoked=TRUE
 FROM scheduled_test_plans p WHERE r.plan_id=p.id AND p.id=ANY($2) AND r.account_id=$1
 AND r.run_id=p.latest_run_id AND r.run_id<>'' AND NOT r.protection_revoked`, accountID, pq.Array(detached)); err != nil {
		return err
	}
	for _, table := range []string{"scheduled_test_protection_states", "scheduled_test_combination_states", "scheduled_test_pass_rate_states"} {
		if _, err := tx.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id=$1 AND plan_id=ANY($2)`, accountID, pq.Array(detached)); err != nil {
			return err
		}
	}
	return reconcileProtectionAccount(ctx, tx, accountID)
}
