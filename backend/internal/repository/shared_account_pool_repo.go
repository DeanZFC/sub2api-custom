package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

type sharedAccountPoolRepository struct{ db *sql.DB }

func NewSharedAccountPoolRepository(_ *dbent.Client, db *sql.DB) service.SharedAccountPoolRepository {
	return &sharedAccountPoolRepository{db: db}
}

// A lateral aggregate fetches recent requests in the same query as the page.
// This avoids nested database reads while holding the page connection open.
const sharedCardSelect = `
SELECT l.id, l.platform, l.display_name, CASE WHEN l.status = 'active' AND (l.account_status <> 'active' OR NOT l.account_schedulable) THEN 'invalid' ELSE l.status END, l.concurrency_limit,
       l.concurrency_multiplier::double precision, l.sell_rate::double precision,
       l.total_call_count, l.last_called_at, recent.calls
FROM (
	 SELECT l.*, a.status AS account_status, a.schedulable AS account_schedulable FROM shared_account_listings l
	 JOIN accounts a ON a.id = l.account_id
	 WHERE l.deleted_at IS NULL AND a.deleted_at IS NULL AND a.account_scope = 'shared'
 %s
 ORDER BY l.total_call_count DESC, l.id DESC LIMIT $1
) l
LEFT JOIN LATERAL (
 SELECT COALESCE(jsonb_agg(to_jsonb(c) ORDER BY c.created_at DESC, c.request_id DESC), '[]'::jsonb) AS calls
 FROM (
  SELECT request_id, COALESCE(model, '') AS model, result_status,
         COALESCE(duration_ms, 0) AS duration_ms, charged_amount, created_at
  FROM shared_account_call_stats WHERE listing_id = l.id
  ORDER BY created_at DESC, request_id DESC LIMIT $2
 ) c
) recent ON TRUE
ORDER BY l.total_call_count DESC, l.id DESC`

func (r *sharedAccountPoolRepository) listCards(ctx context.Context, ownerID *int64, platform string, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if recentLimit <= 0 || recentLimit > 20 {
		recentLimit = 5
	}
	where, args := " AND l.status = 'active' AND a.status = 'active' AND a.schedulable = TRUE", []any{limit, recentLimit}
	if ownerID != nil {
		if *ownerID <= 0 {
			return nil, service.ErrUserNotFound
		}
		where = " AND l.owner_user_id = $3 AND l.status <> 'deleted'"
		args = append(args, *ownerID)
	}
	if platform != "" {
		where += fmt.Sprintf(" AND l.platform = $%d", len(args)+1)
		args = append(args, platform)
	}
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(sharedCardSelect, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]service.SharedAccountCard, 0)
	for rows.Next() {
		var c service.SharedAccountCard
		var calls []byte
		if err := rows.Scan(&c.ID, &c.Platform, &c.DisplayName, &c.Status, &c.ConcurrencyLimit, &c.ConcurrencyMultiplier, &c.SellRate, &c.TotalCallCount, &c.LastCalledAt, &calls); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(calls, &c.RecentCalls); err != nil {
			return nil, fmt.Errorf("decode shared account calls: %w", err)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *sharedAccountPoolRepository) ListPublicCards(ctx context.Context, platform string, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	return r.listCards(ctx, nil, platform, limit, recentLimit)
}
func (r *sharedAccountPoolRepository) GetOwnerCards(ctx context.Context, ownerID int64, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	return r.listCards(ctx, &ownerID, "", limit, recentLimit)
}

func (r *sharedAccountPoolRepository) CreateListing(ctx context.Context, l *service.SharedAccountListing) error {
	if l == nil || l.OwnerUserID <= 0 || l.AccountID <= 0 {
		return service.ErrUserNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	err = tx.QueryRowContext(ctx, `INSERT INTO shared_account_listings(owner_user_id,account_id,platform,display_name,status,concurrency_limit,concurrency_multiplier,sell_rate) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING id`, l.OwnerUserID, l.AccountID, l.Platform, l.DisplayName, l.Status, l.ConcurrencyLimit, l.ConcurrencyMultiplier, l.SellRate).Scan(&l.ID)
	if err != nil {
		return err
	}
	result, err := tx.ExecContext(ctx, `UPDATE accounts SET schedulable=TRUE,updated_at=NOW() WHERE id=$1 AND account_scope='shared' AND deleted_at IS NULL AND (proxy_id IS NULL OR EXISTS (SELECT 1 FROM proxies p WHERE p.id=accounts.proxy_id AND p.owner_user_id=$2))`, l.AccountID, l.OwnerUserID)
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return service.ErrSharedListingNotFound
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &l.AccountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()

}
func (r *sharedAccountPoolRepository) SetListingStatus(ctx context.Context, ownerID, listingID int64, status string) error {
	if ownerID <= 0 || listingID <= 0 {
		return service.ErrUserNotFound
	}
	if status != "active" && status != "paused" {
		return errors.New("invalid shared listing status")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	err = tx.QueryRowContext(ctx, `UPDATE shared_account_listings SET status=$3,updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL RETURNING account_id`, listingID, ownerID, status).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrSharedListingNotFound
	}
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=$2,updated_at=NOW() WHERE id=$1 AND account_scope='shared'`, accountID, status == "active")
	if err != nil {
		return err
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}
func (r *sharedAccountPoolRepository) DeleteListing(ctx context.Context, ownerID, listingID int64) error {
	if ownerID <= 0 || listingID <= 0 {
		return service.ErrUserNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	err = tx.QueryRowContext(ctx, `UPDATE shared_account_listings SET status='deleted',deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL RETURNING account_id`, listingID, ownerID).Scan(&accountID)
	if errors.Is(err, sql.ErrNoRows) {
		return service.ErrSharedListingNotFound
	}
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=FALSE,updated_at=NOW() WHERE id=$1 AND account_scope='shared'`, accountID); err != nil {
		return err
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}
