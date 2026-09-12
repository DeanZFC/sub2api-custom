package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

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
SELECT l.id, l.account_id, l.platform, l.account_type, l.display_name, l.uploader_name, CASE WHEN l.status = 'active' AND (l.account_status <> 'active' OR NOT l.account_schedulable) THEN 'invalid' ELSE l.status END, l.listed, l.concurrency_limit,
       l.concurrency_multiplier::double precision, l.sell_rate::double precision,
       l.total_call_count, l.last_called_at, l.available_models, recent.calls
FROM (
		 SELECT l.* , a.status AS account_status, a.schedulable AS account_schedulable, a.type AS account_type, COALESCE(NULLIF(u.username, ''), u.email) AS uploader_name,
		        COALESCE((SELECT jsonb_agg(k ORDER BY k) FROM jsonb_object_keys(COALESCE(a.credentials->'model_mapping', '{}'::jsonb)) AS k), '[]'::jsonb) AS available_models FROM shared_account_listings l
		 JOIN accounts a ON a.id = l.account_id
		 LEFT JOIN users u ON u.id = l.owner_user_id
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
	where, args := " AND l.status = 'active' AND COALESCE(l.listed, TRUE) = TRUE AND a.status = 'active' AND a.schedulable = TRUE", []any{limit, recentLimit}
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
		var calls, models []byte
		if err := rows.Scan(&c.ID, &c.AccountID, &c.Platform, &c.Type, &c.DisplayName, &c.UploaderName, &c.Status, &c.Listed, &c.ConcurrencyLimit, &c.ConcurrencyMultiplier, &c.SellRate, &c.TotalCallCount, &c.LastCalledAt, &models, &calls); err != nil {
			return nil, err
		}
		// Uploader identity is an admin/owner-only field. Public pool cards must
		// never disclose the account owner's name or email.
		if ownerID == nil {
			c.UploaderName = ""
		}
		if err := json.Unmarshal(calls, &c.RecentCalls); err != nil {
			return nil, fmt.Errorf("decode shared account calls: %w", err)
		}
		var mapped []string
		if len(models) > 0 {
			if err := json.Unmarshal(models, &mapped); err != nil {
				return nil, fmt.Errorf("decode shared account models: %w", err)
			}
		}
		for _, model := range mapped {
			if model == "" || strings.Contains(model, "*") {
				continue
			}
			c.AvailableModels = append(c.AvailableModels, model)
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *sharedAccountPoolRepository) ListPublicCards(ctx context.Context, platform string, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	items, err := r.listCards(ctx, nil, platform, limit, recentLimit)
	for i := range items {
		items[i].UploaderName = ""
	}
	return items, err
}
func (r *sharedAccountPoolRepository) GetOwnerCards(ctx context.Context, ownerID int64, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	return r.listCards(ctx, &ownerID, "", limit, recentLimit)
}

// ListAdminCards is the moderation view. It deliberately keeps the account
// scope predicate from sharedCardSelect while allowing paused, invalid and
// unlisted rows to be inspected by administrators.
func (r *sharedAccountPoolRepository) ListAdminCards(ctx context.Context, platform, status, search string, ownerID *int64, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	if recentLimit <= 0 || recentLimit > 20 {
		recentLimit = 5
	}
	where, args := "", []any{limit, recentLimit}
	if strings.TrimSpace(platform) != "" {
		where += fmt.Sprintf(" AND l.platform = $%d", len(args)+1)
		args = append(args, strings.ToLower(strings.TrimSpace(platform)))
	}
	if strings.TrimSpace(status) != "" {
		where += fmt.Sprintf(" AND l.status = $%d", len(args)+1)
		args = append(args, strings.TrimSpace(status))
	}
	if strings.TrimSpace(search) != "" {
		where += fmt.Sprintf(" AND (l.display_name ILIKE $%d OR l.uploader_name ILIKE $%d OR CAST(l.account_id AS TEXT) ILIKE $%d)", len(args)+1, len(args)+1, len(args)+1)
		args = append(args, "%"+strings.TrimSpace(search)+"%")
	}
	if ownerID != nil && *ownerID > 0 {
		where += fmt.Sprintf(" AND l.owner_user_id = $%d", len(args)+1)
		args = append(args, *ownerID)
	}
	return r.listCardsWithWhere(ctx, where, args, limit, recentLimit)
}

func (r *sharedAccountPoolRepository) ListAdminUsers(ctx context.Context, search string, limit int) ([]service.SharedPoolUserSummary, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	query := `SELECT u.id, COALESCE(NULLIF(u.username,''),''), u.email,
 COALESCE(COUNT(l.id),0), COALESCE(u.shared_publish_enabled,TRUE), u.shared_publish_blocked_until, COALESCE(u.shared_publish_block_reason,'')
 FROM users u LEFT JOIN shared_account_listings l ON l.owner_user_id=u.id AND l.deleted_at IS NULL AND l.status <> 'deleted'
 WHERE u.deleted_at IS NULL`
	args := []any{}
	if strings.TrimSpace(search) != "" {
		query += " AND (u.email ILIKE $1 OR u.username ILIKE $1)"
		args = append(args, "%"+strings.TrimSpace(search)+"%")
	}
	query += fmt.Sprintf(" GROUP BY u.id ORDER BY COUNT(l.id) DESC,u.id DESC LIMIT $%d", len(args)+1)
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]service.SharedPoolUserSummary, 0)
	for rows.Next() {
		var item service.SharedPoolUserSummary
		if err := rows.Scan(&item.UserID, &item.Username, &item.Email, &item.SharedAccountCount, &item.PublishEnabled, &item.BlockedUntil, &item.BlockReason); err != nil {
			return nil, err
		}
		out = append(out, item)
	}
	return out, rows.Err()
}

func (r *sharedAccountPoolRepository) listCardsWithWhere(ctx context.Context, where string, args []any, limit, recentLimit int) ([]service.SharedAccountCard, error) {
	rows, err := r.db.QueryContext(ctx, fmt.Sprintf(sharedCardSelect, where), args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]service.SharedAccountCard, 0)
	for rows.Next() {
		var c service.SharedAccountCard
		var calls, models []byte
		if err := rows.Scan(&c.ID, &c.AccountID, &c.Platform, &c.Type, &c.DisplayName, &c.UploaderName, &c.Status, &c.Listed, &c.ConcurrencyLimit, &c.ConcurrencyMultiplier, &c.SellRate, &c.TotalCallCount, &c.LastCalledAt, &models, &calls); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(calls, &c.RecentCalls); err != nil {
			return nil, fmt.Errorf("decode shared account calls: %w", err)
		}
		var mapped []string
		if len(models) > 0 {
			if err := json.Unmarshal(models, &mapped); err != nil {
				return nil, fmt.Errorf("decode shared account models: %w", err)
			}
		}
		for _, model := range mapped {
			if model != "" && !strings.Contains(model, "*") {
				c.AvailableModels = append(c.AvailableModels, model)
			}
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (r *sharedAccountPoolRepository) GetOwnerListingByAccountID(ctx context.Context, ownerID, accountID int64) (*service.SharedAccountListing, error) {
	if ownerID <= 0 || accountID <= 0 {
		return nil, service.ErrSharedListingNotFound
	}
	var listing service.SharedAccountListing
	err := r.db.QueryRowContext(ctx, `SELECT id, owner_user_id, account_id, platform, display_name, status, concurrency_limit, concurrency_multiplier::double precision, sell_rate::double precision, total_call_count FROM shared_account_listings WHERE account_id=$1 AND owner_user_id=$2 AND deleted_at IS NULL AND status <> 'deleted'`, accountID, ownerID).Scan(&listing.ID, &listing.OwnerUserID, &listing.AccountID, &listing.Platform, &listing.DisplayName, &listing.Status, &listing.ConcurrencyLimit, &listing.ConcurrencyMultiplier, &listing.SellRate, &listing.TotalCallCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSharedListingNotFound
	}
	if err != nil {
		return nil, err
	}
	return &listing, nil
}

func (r *sharedAccountPoolRepository) UpdateListingMeta(ctx context.Context, ownerID, listingID int64, displayName string, concurrency int, sellRate float64) error {
	if ownerID <= 0 || listingID <= 0 {
		return service.ErrSharedListingNotFound
	}
	result, err := r.db.ExecContext(ctx, `UPDATE shared_account_listings SET display_name=$3, concurrency_limit=$4, sell_rate=$5, updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL AND status <> 'deleted'`, listingID, ownerID, displayName, concurrency, sellRate)
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
	return nil
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
	// Resuming a shared listing is an explicit owner action.  Clear a prior
	// runtime error as part of that action so an account that was temporarily
	// marked unavailable can return to the same active state as a newly created
	// account.  A real credential failure will be marked unavailable again by
	// the gateway on the next request.
	_, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=$2, status=CASE WHEN $2 THEN 'active' ELSE status END, updated_at=NOW() WHERE id=$1 AND account_scope='shared'`, accountID, status == "active")
	if err != nil {
		return err
	}
	if err := enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sharedAccountPoolRepository) SetListingListed(ctx context.Context, ownerID, listingID int64, listed bool) error {
	if ownerID <= 0 || listingID <= 0 {
		return service.ErrUserNotFound
	}
	result, err := r.db.ExecContext(ctx, `UPDATE shared_account_listings SET listed=$3, updated_at=NOW() WHERE id=$1 AND owner_user_id=$2 AND deleted_at IS NULL AND status <> 'deleted'`, listingID, ownerID, listed)
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
	return nil
}

func (r *sharedAccountPoolRepository) SetListingAdminStatus(ctx context.Context, listingID int64, status string) error {
	if listingID <= 0 || (status != "active" && status != "paused" && status != "suspended" && status != "invalid") {
		return errors.New("invalid shared listing status")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	var listed bool
	if err = tx.QueryRowContext(ctx, `UPDATE shared_account_listings SET status=$2,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL RETURNING account_id,COALESCE(listed,TRUE)`, listingID, status).Scan(&accountID, &listed); errors.Is(err, sql.ErrNoRows) {
		return service.ErrSharedListingNotFound
	} else if err != nil {
		return err
	}
	// Suspended/invalid rows must leave scheduling immediately. Resuming an
	// active row restores persistent schedulability only for shared accounts.
	sched := status == "active" && listed
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=$2, status=CASE WHEN $2 THEN 'active' ELSE status END, updated_at=NOW() WHERE id=$1 AND account_scope='shared'`, accountID, sched); err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sharedAccountPoolRepository) SetListingAdminListed(ctx context.Context, listingID int64, listed bool) error {
	if listingID <= 0 {
		return service.ErrSharedListingNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var accountID int64
	var status string
	if err = tx.QueryRowContext(ctx, `UPDATE shared_account_listings SET listed=$2,updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL RETURNING account_id,status`, listingID, listed).Scan(&accountID, &status); errors.Is(err, sql.ErrNoRows) {
		return service.ErrSharedListingNotFound
	} else if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE accounts SET schedulable=($2 AND $3='active' AND status='active'),updated_at=NOW() WHERE id=$1 AND account_scope='shared'`, accountID, listed, status); err != nil {
		return err
	}
	if err = enqueueSchedulerOutbox(ctx, tx, service.SchedulerOutboxEventAccountChanged, &accountID, nil, nil); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *sharedAccountPoolRepository) SetUserSharedPublishPermission(ctx context.Context, userID int64, enabled bool, reason string, until *time.Time) error {
	if userID <= 0 {
		return service.ErrUserNotFound
	}
	res, err := r.db.ExecContext(ctx, `UPDATE users SET shared_publish_enabled=$2, shared_publish_block_reason=$3, shared_publish_blocked_until=$4, updated_at=NOW() WHERE id=$1 AND deleted_at IS NULL`, userID, enabled, strings.TrimSpace(reason), until)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return service.ErrUserNotFound
	}
	return nil
}

func (r *sharedAccountPoolRepository) IsUserSharedPublishAllowed(ctx context.Context, userID int64) (bool, error) {
	if userID <= 0 {
		return false, service.ErrUserNotFound
	}
	var enabled bool
	err := r.db.QueryRowContext(ctx, `SELECT COALESCE(shared_publish_enabled, TRUE) AND (shared_publish_blocked_until IS NULL OR shared_publish_blocked_until <= NOW()) FROM users WHERE id=$1 AND deleted_at IS NULL`, userID).Scan(&enabled)
	if errors.Is(err, sql.ErrNoRows) {
		return false, service.ErrUserNotFound
	}
	return enabled, err
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
