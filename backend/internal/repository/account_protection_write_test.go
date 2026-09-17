package repository

import (
	"context"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
)

func TestAccountProtectionBulkProxyRechecksLockedState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectBegin()
	// A stale admin read saw protection disabled; the row locked by the actual
	// write now has protection enabled. No UPDATE or proxy-pool write may follow.
	mock.ExpectQuery(`SELECT platform,type,extra FROM accounts.*FOR NO KEY UPDATE`).
		WithArgs("{1,2}").
		WillReturnRows(sqlmock.NewRows([]string{"platform", "type", "extra"}).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, []byte(`{}`)).
			AddRow(service.PlatformOpenAI, service.AccountTypeOAuth, []byte(`{"anti_degradation":true}`)))
	mock.ExpectRollback()
	primary := int64(10)
	_, err = newAccountRepositoryWithSQL(client, db, nil).BulkUpdate(context.Background(), []int64{1, 2}, service.AccountBulkUpdate{
		ProxyID: &primary, ProxyIDs: &[]int64{10, 20},
	})
	require.ErrorIs(t, err, service.ErrProtectedProxyModeChange)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestAccountProtectionOrdinaryWriteRechecksLockedProxyState(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { _ = client.Close() })
	mock.ExpectQuery(`SELECT extra FROM accounts WHERE id = \$1`).WithArgs(int64(1)).
		WillReturnRows(sqlmock.NewRows([]string{"extra"}).AddRow([]byte(`{"anti_degradation":true}`)))
	err = preserveLockedAccountProtection(context.Background(), client, &service.Account{
		ID: 1, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		Extra: map[string]any{}, ProxyIDs: []int64{10, 20}, ProxyPoolChanged: true,
	})
	require.ErrorIs(t, err, service.ErrProtectedProxyModeChange)
	require.NoError(t, mock.ExpectationsWereMet())
}
