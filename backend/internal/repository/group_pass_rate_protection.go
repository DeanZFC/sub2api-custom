package repository

import (
	"context"

	"github.com/lib/pq"
)

// Group removals follow the same advisory -> plan -> account lock order as
// manual account membership edits, before locking or deleting the group.
func lockGroupPassRateProtection(ctx context.Context, tx sqlExecutor, groupID int64) ([]int64, error) {
	if _, err := tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006960,1735553655)`); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT p.id FROM scheduled_test_plans p WHERE $1=ANY(p.group_ids)
 ORDER BY p.id FOR NO KEY UPDATE`, groupID)
	if err != nil {
		return nil, err
	}
	var plans []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return nil, err
		}
		plans = append(plans, id)
	}
	err = rows.Err()
	if closeErr := rows.Close(); err == nil {
		err = closeErr
	}
	if err != nil || len(plans) == 0 {
		return plans, err
	}
	rows, err = tx.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE EXISTS (
 SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=a.id AND s.plan_id=ANY($1))
 OR EXISTS (SELECT 1 FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id=r.plan_id
 WHERE r.account_id=a.id AND p.id=ANY($1) AND r.run_id=p.latest_run_id AND r.run_id<>'' AND NOT r.protection_revoked)
 ORDER BY a.id FOR NO KEY UPDATE`, pq.Array(plans))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
	}
	return plans, rows.Err()
}

func releaseDetachedGroupPassRates(ctx context.Context, tx sqlExecutor, planIDs []int64) error {
	if len(planIDs) == 0 {
		return nil
	}
	// Revoke unfinished rounds too: an account may not have a pass-rate state
	// until its first response arrives after the administrator removes a group.
	if _, err := tx.ExecContext(ctx, `UPDATE scheduled_test_results r SET protection_revoked=TRUE FROM scheduled_test_plans p
 WHERE r.plan_id=p.id AND p.id=ANY($1) AND r.run_id=p.latest_run_id AND r.run_id<>'' AND NOT r.protection_revoked
 AND NOT EXISTS (SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
 WHERE ag.account_id=r.account_id AND ag.group_id=ANY(p.group_ids) AND g.deleted_at IS NULL)`, pq.Array(planIDs)); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `DELETE FROM scheduled_test_pass_rate_states s USING scheduled_test_plans p
 WHERE s.plan_id=p.id AND p.id=ANY($1) AND NOT EXISTS (
  SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
  WHERE ag.account_id=s.account_id AND ag.group_id=ANY(p.group_ids) AND g.deleted_at IS NULL)`, pq.Array(planIDs))
	return err
}
