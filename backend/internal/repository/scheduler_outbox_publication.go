package repository

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"slices"
)

// Hold account row locks while delayed compensation publishes fresh state.
// Concurrent updates/deletion cannot be overwritten using the retry's old read.
func (r *accountRepository) WithLatestSchedulerAccounts(ctx context.Context, ids []int64, publish func([]service.Account) error) error {
	if len(ids) == 0 {
		return publish(nil)
	}
	ids = slices.Clone(ids)
	slices.Sort(ids)
	ids = slices.Compact(ids)
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()
	rows, err := tx.Client().QueryContext(ctx, "SELECT id FROM accounts WHERE id = ANY($1) ORDER BY id FOR UPDATE", pq.Array(ids))
	if err != nil {
		return err
	}
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			_ = rows.Close()
			return err
		}
	}
	err = rows.Err()
	_ = rows.Close()
	if err != nil {
		return err
	}
	scoped := *r
	scoped.client = tx.Client()
	scoped.sql = tx.Client()
	scoped.schedulerCache = nil
	accounts, err := scoped.GetByIDs(ctx, ids)
	if err != nil {
		return err
	}
	values := make([]service.Account, 0, len(accounts))
	for _, a := range accounts {
		values = append(values, *a)
	}
	if err := publish(values); err != nil {
		return err
	}
	return tx.Commit()
}
