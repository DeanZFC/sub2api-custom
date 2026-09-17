package repository

import (
	"context"
	"fmt"
	"strings"
	"time"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

var _ service.AccountHealthAccountStore = (*accountRepository)(nil)

func (r *accountRepository) TrySetAccountHealthIsolation(ctx context.Context, id int64, until time.Time, reason string, automatic bool) (bool, error) {
	if !strings.HasPrefix(reason, "health:") || (automatic && !strings.HasPrefix(reason, "health:auto")) {
		return false, fmt.Errorf("account health isolation reason is invalid")
	}
	return r.changeAccountHealthIsolation(ctx, id, `
		WITH changed AS (
			UPDATE accounts
			SET temp_unschedulable_until = $1, temp_unschedulable_reason = $2, updated_at = NOW()
			WHERE id = $3 AND deleted_at IS NULL
			  AND (temp_unschedulable_until IS NULL OR temp_unschedulable_until <= NOW()
			       OR (NOT $4::boolean AND temp_unschedulable_reason LIKE 'health:%'))
			  AND (NOT $4::boolean OR (
			       extra #> '{account_protection_policy,enabled}' = 'true'::jsonb
			       AND lower(btrim(extra #>> '{account_protection_policy,mode}')) = 'enforce'
			       AND (rate_limit_reset_at IS NULL OR rate_limit_reset_at <= NOW())
			       AND (overload_until IS NULL OR overload_until <= NOW())))
			RETURNING id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $5, changed.id, NULL, NULL FROM changed
	`, until, reason, id, automatic, service.SchedulerOutboxEventAccountChanged)
}

func (r *accountRepository) ClearAccountHealthIsolation(ctx context.Context, id int64, automatic bool) (bool, error) {
	prefix := "health:"
	if automatic {
		prefix = "health:auto"
	}
	return r.changeAccountHealthIsolation(ctx, id, `
		WITH changed AS (
			UPDATE accounts
			SET temp_unschedulable_until = NULL, temp_unschedulable_reason = NULL, updated_at = NOW()
			WHERE id = $1 AND deleted_at IS NULL
			  AND temp_unschedulable_reason LIKE $2 || '%'
			RETURNING id
		)
		INSERT INTO scheduler_outbox (event_type, account_id, group_id, payload)
		SELECT $3, changed.id, NULL, NULL FROM changed
	`, id, prefix, service.SchedulerOutboxEventAccountChanged)
}

func (r *accountRepository) changeAccountHealthIsolation(ctx context.Context, id int64, query string, args ...any) (bool, error) {
	exec := r.sql
	tx := dbent.TxFromContext(ctx)
	if tx != nil {
		exec = tx.Client()
	}
	result, err := exec.ExecContext(ctx, query, args...)
	if err != nil {
		return false, err
	}
	affected, err := result.RowsAffected()
	if err != nil || affected == 0 {
		return false, err
	}
	if tx == nil {
		r.syncSchedulerAccountSnapshotDetached(ctx, id)
	}
	return true, nil
}
