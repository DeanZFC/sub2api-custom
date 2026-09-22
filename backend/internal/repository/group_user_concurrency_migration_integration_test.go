//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestMigration266GroupUserConcurrencyLimit(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	_, err := tx.ExecContext(ctx, `ALTER TABLE groups DROP COLUMN user_concurrency_limit`)
	require.NoError(t, err)
	var groupID int64
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups (name) VALUES ('migration-group-concurrency') RETURNING id`).Scan(&groupID))

	migrationSQL, err := dbmigrations.FS.ReadFile("266_restore_group_user_concurrency_limit.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	var limit int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT user_concurrency_limit FROM groups WHERE id = $1`, groupID).Scan(&limit))
	require.Zero(t, limit, "existing groups default to unlimited")

	_, err = tx.ExecContext(ctx, `UPDATE groups SET user_concurrency_limit = 7 WHERE id = $1`, groupID)
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, err)
		require.NoError(t, tx.QueryRowContext(ctx, `SELECT user_concurrency_limit FROM groups WHERE id = $1`, groupID).Scan(&limit))
		require.Equal(t, 7, limit, "migration replay preserves configured limits")
	}

	_, err = tx.ExecContext(ctx, `UPDATE groups SET user_concurrency_limit = -1 WHERE id = $1`, groupID)
	var constraintErr *pq.Error
	require.ErrorAs(t, err, &constraintErr)
	require.Equal(t, pq.ErrorCode("23514"), constraintErr.Code)
	require.Equal(t, "groups_user_concurrency_limit_nonnegative", constraintErr.Constraint)
}
