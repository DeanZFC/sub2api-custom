package repository

import (
	"context"
	"errors"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

var _ service.AccountQualityProtectionRecoverer = (*accountRepository)(nil)

// ClearAccountQualityProtection is an explicit administrator override. Keep
// policy/history intact, but fence the current round so late results cannot
// immediately reinstate its holds. A new round may protect the account again.
func (r *accountRepository) ClearAccountQualityProtection(ctx context.Context, accountID int64) error {
	if scoped := r.accountTransactionScope(ctx); scoped != r {
		return scoped.ClearAccountQualityProtection(ctx, accountID)
	}
	tx, err := r.client.Tx(ctx)
	if err != nil && !errors.Is(err, dbent.ErrTxStarted) {
		return err
	}
	client := r.client
	if tx != nil {
		defer func() { _ = tx.Rollback() }()
		client = tx.Client()
	}
	plans, err := lockAccountMembershipProtection(ctx, client, accountID)
	if err != nil {
		return translatePersistenceError(err, service.ErrAccountNotFound, nil)
	}
	if _, err := client.ExecContext(ctx, `UPDATE scheduled_test_results r SET protection_revoked=TRUE
 FROM scheduled_test_plans p WHERE r.plan_id=p.id AND p.id=ANY($2) AND r.account_id=$1
 AND r.run_id=p.latest_run_id AND r.run_id<>'' AND NOT r.protection_revoked`, accountID, pq.Array(plans)); err != nil {
		return err
	}
	for _, table := range []string{"scheduled_test_protection_states", "scheduled_test_combination_states", "scheduled_test_pass_rate_states", "scheduled_test_all_pass_rounds"} {
		if _, err := client.ExecContext(ctx, `DELETE FROM `+table+` WHERE account_id=$1`, accountID); err != nil {
			return err
		}
	}
	// Reconcile even when no source rows remain, to remove stale projections.
	if err := syncActivePassRateProjection(ctx, client, accountID); err != nil {
		return err
	}
	// Release the quality status even if manually unschedulable, without
	// enabling scheduling or changing any other administrator status.
	if _, err := client.ExecContext(ctx, `UPDATE accounts SET status='active',updated_at=clock_timestamp()
 WHERE id=$1 AND status='quality_paused' AND deleted_at IS NULL`, accountID); err != nil {
		return err
	}
	if err := enqueueSchedulerOutbox(ctx, client, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	if tx != nil {
		if err := tx.Commit(); err != nil {
			return err
		}
		r.syncSchedulerAccountSnapshotDetached(ctx, accountID)
	}
	return nil
}
