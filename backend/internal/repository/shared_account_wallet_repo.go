package repository

import (
	"context"
	"database/sql"
	"errors"

	"github.com/Wei-Shaw/sub2api/internal/service"
)

type sharedWalletRepository struct{ db *sql.DB }

func NewSharedWalletRepository(db *sql.DB) service.SharedWalletRepository {
	return &sharedWalletRepository{db}
}

// Lock the user first in every wallet mutation so concurrent credits and transfers
// serialize consistently. A deleted user cannot open or transfer a wallet.
func (r *sharedWalletRepository) beginWallet(ctx context.Context, userID int64) (*sql.Tx, error) {
	if userID <= 0 {
		return nil, service.ErrUserNotFound
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	var id int64
	if err = tx.QueryRowContext(ctx, `SELECT id FROM users WHERE id=$1 AND deleted_at IS NULL FOR UPDATE`, userID).Scan(&id); err != nil {
		tx.Rollback()
		if errors.Is(err, sql.ErrNoRows) {
			return nil, service.ErrUserNotFound
		}
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO shared_account_wallets(user_id) VALUES($1) ON CONFLICT(user_id) DO NOTHING`, userID); err != nil {
		tx.Rollback()
		return nil, err
	}
	if err = tx.QueryRowContext(ctx, `SELECT user_id FROM shared_account_wallets WHERE user_id=$1 FOR UPDATE`, userID).Scan(&id); err != nil {
		tx.Rollback()
		return nil, err
	}
	return tx, nil
}

// released_at distinguishes an already released earning from one with no hold.
// Changing a listing status cannot erase earned revenue or release it twice.
const sharedReleaseSQL = `WITH released AS (
 UPDATE shared_account_usage_ledger SET released_at=NOW(),updated_at=NOW()
 WHERE owner_user_id=$1 AND action='earn' AND released_at IS NULL
   AND frozen_until IS NOT NULL AND frozen_until<=NOW()
 RETURNING id,listing_id,owner_amount
), journal AS (
 INSERT INTO shared_account_wallet_ledger(user_id,listing_id,usage_ledger_id,action,amount,idempotency_key)
 SELECT $1,listing_id,id,'release',owner_amount,'release:'||id::text FROM released
 RETURNING amount
)
UPDATE shared_account_wallets SET pending_amount=pending_amount-(SELECT COALESCE(SUM(amount),0) FROM journal),
 available_amount=available_amount+(SELECT COALESCE(SUM(amount),0) FROM journal), updated_at=NOW() WHERE user_id=$1`

func (r *sharedWalletRepository) GetWallet(ctx context.Context, userID int64) (*service.SharedWallet, error) {
	tx, err := r.beginWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err = tx.ExecContext(ctx, sharedReleaseSQL, userID); err != nil {
		return nil, err
	}
	var w service.SharedWallet
	err = tx.QueryRowContext(ctx, `SELECT pending_amount::text,available_amount::text,frozen_amount::text,total_earned::text,total_transferred::text FROM shared_account_wallets WHERE user_id=$1`, userID).Scan(&w.Pending, &w.Available, &w.Frozen, &w.TotalEarned, &w.TotalTransferred)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &w, nil
}

func (r *sharedWalletRepository) TransferEarnings(ctx context.Context, userID int64, key string) (*service.SharedTransfer, error) {
	tx, err := r.beginWallet(ctx, userID)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var result service.SharedTransfer
	err = tx.QueryRowContext(ctx, `SELECT id,amount::text,balance_after::text FROM shared_account_withdrawals WHERE user_id=$1 AND idempotency_key=$2`, userID, key).Scan(&result.ID, &result.Amount, &result.BalanceAfter)
	if err == nil {
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return &result, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, sharedReleaseSQL, userID); err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `SELECT available_amount::text FROM shared_account_wallets WHERE user_id=$1 AND available_amount>0`, userID).Scan(&result.Amount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, service.ErrSharedEarningsEmpty
	}
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, `UPDATE shared_account_wallets SET available_amount=0,total_transferred=total_transferred+$2::numeric,updated_at=NOW() WHERE user_id=$1`, userID, result.Amount); err != nil {
		return nil, err
	}
	// This is an internal earnings transfer, not a recharge or affiliate event.
	err = tx.QueryRowContext(ctx, `UPDATE users SET balance=balance+$2::numeric,updated_at=NOW() WHERE id=$1 RETURNING balance::text`, userID, result.Amount).Scan(&result.BalanceAfter)
	if err != nil {
		return nil, err
	}
	var journalID int64
	err = tx.QueryRowContext(ctx, `INSERT INTO shared_account_wallet_ledger(user_id,action,amount,available_after) VALUES($1,'transfer_to_balance',$2::numeric,0) RETURNING id`, userID, result.Amount).Scan(&journalID)
	if err != nil {
		return nil, err
	}
	err = tx.QueryRowContext(ctx, `INSERT INTO shared_account_withdrawals(user_id,amount,status,ledger_id,processed_at,idempotency_key,balance_after) VALUES($1,$2::numeric,'completed',$3,NOW(),$4,$5::numeric) RETURNING id`, userID, result.Amount, journalID, key, result.BalanceAfter).Scan(&result.ID)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return &result, nil
}
