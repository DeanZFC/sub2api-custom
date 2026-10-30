//go:build unit

package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestAdminAccountNamesBatch(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	repo := &scheduledTestResultRepository{db: db}
	mock.ExpectQuery(`SELECT id, COALESCE\(name, ''\) FROM accounts WHERE id = ANY\(\$1\) AND deleted_at IS NULL`).
		WithArgs("{2296,88,99}").
		WillReturnRows(sqlmock.NewRows([]string{"id", "name"}).AddRow(2296, "GPT-SN-1").AddRow(88, "renamed current account"))
	names, err := repo.GetAdminResultAccountNames(context.Background(), []int64{2296, 88, 99})
	require.NoError(t, err)
	require.Equal(t, map[int64]string{2296: "GPT-SN-1", 88: "renamed current account"}, names)
	require.NotContains(t, names, int64(99), "deleted or missing accounts have no current name")
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestAdminAccountNamesEmptyDoesNotQuery(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	names, err := (&scheduledTestResultRepository{db: db}).GetAdminResultAccountNames(context.Background(), nil)
	require.NoError(t, err)
	require.Empty(t, names)
	require.NoError(t, mock.ExpectationsWereMet())
}

func TestScheduledTestAdminAccountNamesPropagatesLookupError(t *testing.T) {
	db, mock, err := sqlmock.New()
	require.NoError(t, err)
	defer func() { _ = db.Close() }()
	expected := errors.New("lookup failed")
	mock.ExpectQuery(`SELECT id, COALESCE`).WithArgs("{2296}").WillReturnError(expected)
	names, err := (&scheduledTestResultRepository{db: db}).GetAdminResultAccountNames(context.Background(), []int64{2296})
	require.ErrorIs(t, err, expected)
	require.Nil(t, names)
	require.NoError(t, mock.ExpectationsWereMet())
}
