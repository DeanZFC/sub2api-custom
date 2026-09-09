package repository

import (
	"context"
	"database/sql"
	"errors"
	"regexp"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func expectSharedWalletLock(mock sqlmock.Sqlmock) {
	mock.ExpectBegin()
	mock.ExpectQuery("SELECT id FROM users").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"id"}).AddRow(42))
	mock.ExpectExec("INSERT INTO shared_account_wallets").WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT user_id FROM shared_account_wallets").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"user_id"}).AddRow(42))
}
func TestSharedTransferRetryReturnsOriginalWithoutCreditingAgain(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectSharedWalletLock(mock)
	mock.ExpectQuery("SELECT id,amount::text,balance_after::text").WithArgs(int64(42), "request-123").WillReturnRows(sqlmock.NewRows([]string{"id", "amount", "balance_after"}).AddRow(7, "90.00000000", "95.00000000"))
	mock.ExpectCommit()
	r := sharedWalletRepository{db: db}
	result, err := r.TransferEarnings(context.Background(), 42, "request-123")
	require.NoError(t, err)
	require.Equal(t, "90.00000000", result.Amount)
	require.EqualValues(t, 7, result.ID)
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSharedTransferAuditFailureRollsBackBalanceAndWallet(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectSharedWalletLock(mock)
	mock.ExpectQuery("SELECT id,amount::text,balance_after::text").WithArgs(int64(42), "request-123").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta(sharedReleaseSQL)).WithArgs(int64(42)).WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("SELECT available_amount::text").WithArgs(int64(42)).WillReturnRows(sqlmock.NewRows([]string{"amount"}).AddRow("90.00000001"))
	mock.ExpectExec("UPDATE shared_account_wallets SET available_amount=0").WithArgs(int64(42), "90.00000001").WillReturnResult(sqlmock.NewResult(0, 1))
	mock.ExpectQuery("UPDATE users SET balance").WithArgs(int64(42), "90.00000001").WillReturnRows(sqlmock.NewRows([]string{"balance"}).AddRow("95.00000001"))
	mock.ExpectQuery("INSERT INTO shared_account_wallet_ledger").WithArgs(int64(42), "90.00000001").WillReturnError(errors.New("journal write failed"))
	mock.ExpectRollback()
	r := sharedWalletRepository{db: db}
	result, err := r.TransferEarnings(context.Background(), 42, "request-123")
	require.Nil(t, result)
	require.ErrorContains(t, err, "journal write failed")
	require.NoError(t, mock.ExpectationsWereMet())
}
func TestSharedTransferEmptyDoesNotCreditBalance(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer db.Close()
	expectSharedWalletLock(mock)
	mock.ExpectQuery("SELECT id,amount::text,balance_after::text").WillReturnError(sql.ErrNoRows)
	mock.ExpectExec(regexp.QuoteMeta(sharedReleaseSQL)).WillReturnResult(sqlmock.NewResult(0, 0))
	mock.ExpectQuery("SELECT available_amount::text").WillReturnError(sql.ErrNoRows)
	mock.ExpectRollback()
	r := sharedWalletRepository{db: db}
	_, err = r.TransferEarnings(context.Background(), 42, "request-123")
	require.ErrorIs(t, err, service.ErrSharedEarningsEmpty)
	require.NoError(t, mock.ExpectationsWereMet())
}
