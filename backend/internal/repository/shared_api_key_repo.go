package repository

import (
	"context"
	"database/sql"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"strings"
	"time"
)

type sharedAPIKeyRepository struct{ db *sql.DB }

func NewSharedAPIKeyRepository(db *sql.DB) service.SharedAPIKeyRepository {
	return &sharedAPIKeyRepository{db: db}
}
func previewKey(k string) string {
	if len(k) <= 10 {
		return k
	}
	return k[:6] + "…" + k[len(k)-4:]
}
func scanSharedKey(row interface{ Scan(...any) error }, k *service.SharedAPIKey) error {
	if err := row.Scan(&k.ID, &k.UserID, &k.Name, &k.Key, &k.Platform, &k.Status, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return err
	}
	k.KeyPreview = previewKey(k.Key)
	k.Key = ""
	return nil
}
func (r *sharedAPIKeyRepository) loadListings(ctx context.Context, id int64) ([]int64, []int64, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT listing_id,account_id FROM shared_api_key_listings k JOIN shared_account_listings l ON l.id=k.listing_id WHERE k.api_key_id=$1 ORDER BY k.position,k.listing_id`, id)
	if err != nil {
		return nil, nil, err
	}
	defer rows.Close()
	var out, accounts []int64
	for rows.Next() {
		var v, a int64
		if err := rows.Scan(&v, &a); err != nil {
			return nil, nil, err
		}
		out = append(out, v)
		accounts = append(accounts, a)
	}
	return out, accounts, rows.Err()
}
func (r *sharedAPIKeyRepository) Create(ctx context.Context, k *service.SharedAPIKey) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err = tx.QueryRowContext(ctx, `INSERT INTO shared_api_keys(user_id,name,key,platform,status) VALUES($1,$2,$3,$4,$5) RETURNING id,created_at,updated_at`, k.UserID, k.Name, k.Key, k.Platform, k.Status).Scan(&k.ID, &k.CreatedAt, &k.UpdatedAt); err != nil {
		return err
	}
	for i, id := range k.ListingIDs {
		res, e := tx.ExecContext(ctx, `INSERT INTO shared_api_key_listings(api_key_id,listing_id,position) SELECT $1,$2,$3 WHERE EXISTS (SELECT 1 FROM shared_account_listings WHERE id=$2 AND owner_user_id=$4 AND platform=$5 AND status='active' AND deleted_at IS NULL)`, k.ID, id, i, k.UserID, k.Platform)
		if e != nil {
			err = e
			return err
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return service.ErrSharedAPIKeyNotFound
		}
	}
	return tx.Commit()
}
func (r *sharedAPIKeyRepository) ListByUser(ctx context.Context, userID int64) ([]service.SharedAPIKey, error) {
	rows, err := r.db.QueryContext(ctx, `SELECT id,user_id,name,key,platform,status,created_at,updated_at FROM shared_api_keys WHERE user_id=$1 AND deleted_at IS NULL ORDER BY created_at DESC`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []service.SharedAPIKey{}
	for rows.Next() {
		var k service.SharedAPIKey
		if err := scanSharedKey(rows, &k); err != nil {
			return nil, err
		}
		var e error
		k.ListingIDs, k.ListingAccountIDs, e = r.loadListings(ctx, k.ID)
		if e != nil {
			return nil, e
		}
		out = append(out, k)
	}
	return out, rows.Err()
}
func (r *sharedAPIKeyRepository) GetByID(ctx context.Context, userID, id int64) (*service.SharedAPIKey, error) {
	row := r.db.QueryRowContext(ctx, `SELECT id,user_id,name,key,platform,status,created_at,updated_at FROM shared_api_keys WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	k := &service.SharedAPIKey{}
	if err := scanSharedKey(row, k); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrSharedAPIKeyNotFound
		}
		return nil, err
	}
	var err error
	k.ListingIDs, k.ListingAccountIDs, err = r.loadListings(ctx, k.ID)
	return k, err
}
func (r *sharedAPIKeyRepository) GetByKey(ctx context.Context, raw string) (*service.SharedAPIKey, error) {
	row := r.db.QueryRowContext(ctx, `SELECT k.id,k.user_id,k.name,k.key,k.platform,k.status,k.created_at,k.updated_at FROM shared_api_keys k WHERE k.key=$1 AND k.deleted_at IS NULL`, strings.TrimSpace(raw))
	k := &service.SharedAPIKey{}
	if err := scanSharedKey(row, k); err != nil {
		if err == sql.ErrNoRows {
			return nil, service.ErrSharedAPIKeyNotFound
		}
		return nil, err
	}
	var err error
	k.ListingIDs, k.ListingAccountIDs, err = r.loadListings(ctx, k.ID)
	return k, err
}
func (r *sharedAPIKeyRepository) Update(ctx context.Context, k *service.SharedAPIKey) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, `UPDATE shared_api_keys SET name=$1,status=$2,updated_at=NOW() WHERE id=$3 AND user_id=$4 AND deleted_at IS NULL`, k.Name, k.Status, k.ID, k.UserID); err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `DELETE FROM shared_api_key_listings WHERE api_key_id=$1`, k.ID); err != nil {
		return err
	}
	for i, id := range k.ListingIDs {
		res, e := tx.ExecContext(ctx, `INSERT INTO shared_api_key_listings(api_key_id,listing_id,position) SELECT $1,$2,$3 WHERE EXISTS (SELECT 1 FROM shared_account_listings WHERE id=$2 AND owner_user_id=$4 AND platform=$5 AND status='active' AND deleted_at IS NULL)`, k.ID, id, i, k.UserID, k.Platform)
		if e != nil {
			return e
		}
		if n, _ := res.RowsAffected(); n != 1 {
			return service.ErrSharedAPIKeyNotFound
		}
	}
	return tx.Commit()
}
func (r *sharedAPIKeyRepository) Delete(ctx context.Context, userID, id int64) error {
	res, err := r.db.ExecContext(ctx, `UPDATE shared_api_keys SET status='disabled',deleted_at=NOW(),updated_at=NOW() WHERE id=$1 AND user_id=$2 AND deleted_at IS NULL`, id, userID)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return service.ErrSharedAPIKeyNotFound
	}
	return nil
}

var _ = time.Time{}
