//go:build unit

package service

import (
	"context"
	"errors"
	"regexp"
	"testing"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/stretchr/testify/require"
)

type scopedMembershipLockRepository struct {
	AccountRepository
}

func (*scopedMembershipLockRepository) ScopedAdminLockAccountMembership(ctx context.Context, id int64) error {
	_, err := dbent.TxFromContext(ctx).Client().ExecContext(ctx, "SELECT membership_plan_lock($1)", id)
	return err
}

func TestScopedAdminTransactionLocksMembershipBeforeAccount(t *testing.T) {
	for _, lockFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "locks in order", true: "lock failure rolls back"}[lockFails], func(t *testing.T) {
			db, mock, err := sqlmock.New()
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
			t.Cleanup(func() { _ = client.Close() })
			s := &adminServiceImpl{entClient: client, accountRepo: &scopedMembershipLockRepository{}}
			mock.ExpectBegin()
			mock.ExpectExec(regexp.QuoteMeta("SELECT pg_advisory_xact_lock(hashtextextended('scoped_admin_account:' || $1::text, 0))")).WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 1))
			planLock := mock.ExpectExec(regexp.QuoteMeta("SELECT membership_plan_lock($1)")).WithArgs(int64(41))
			lockErr := errors.New("membership plan lock failed")
			if lockFails {
				planLock.WillReturnError(lockErr)
				mock.ExpectRollback()
			} else {
				planLock.WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta("SELECT id FROM accounts WHERE id = $1 AND deleted_at IS NULL FOR UPDATE")).WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectExec(regexp.QuoteMeta("SELECT account_id FROM account_groups WHERE account_id = $1 FOR UPDATE")).WithArgs(int64(41)).WillReturnResult(sqlmock.NewResult(0, 1))
				mock.ExpectCommit()
			}
			called := false
			got, err := s.scopedAdminTransaction(context.Background(), 41, false, func(txctx context.Context) (*Account, error) {
				called = true
				require.NotNil(t, dbent.TxFromContext(txctx))
				return &Account{ID: 41}, nil
			})
			if lockFails {
				require.ErrorIs(t, err, lockErr)
				require.Nil(t, got)
				require.False(t, called)
			} else {
				require.NoError(t, err)
				require.EqualValues(t, 41, got.ID)
				require.True(t, called)
			}
			require.NoError(t, mock.ExpectationsWereMet())
		})
	}
}
