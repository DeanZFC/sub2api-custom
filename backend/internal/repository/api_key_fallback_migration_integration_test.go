//go:build integration

package repository

import (
	"context"
	"database/sql"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestMigration267APIKeyFallbackGroup(t *testing.T) {
	migrationSQL, err := dbmigrations.FS.ReadFile("267_restore_api_key_fallback_group.sql")
	require.NoError(t, err)

	for _, existingColumn := range []bool{false, true} {
		name := "missing_column"
		if existingColumn {
			name = "existing_fallback"
		}
		t.Run(name, func(t *testing.T) {
			tx := testTx(t)
			ctx := context.Background()
			if !existingColumn {
				_, err := tx.ExecContext(ctx, `ALTER TABLE api_keys DROP COLUMN fallback_group_id`)
				require.NoError(t, err)
			}

			var userID, primaryID, fallbackID, keyID int64
			require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO users (email, password_hash) VALUES ('migration-fallback@test.com', 'test-only') RETURNING id`).Scan(&userID))
			require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups (name) VALUES ('migration-primary') RETURNING id`).Scan(&primaryID))
			require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups (name) VALUES ('migration-fallback') RETURNING id`).Scan(&fallbackID))
			require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO api_keys (user_id, key, name, group_id) VALUES ($1, 'sk-migration-fallback', 'Migration key', $2) RETURNING id`, userID, primaryID).Scan(&keyID))

			if existingColumn {
				_, err := tx.ExecContext(ctx, `UPDATE api_keys SET fallback_group_id = $1 WHERE id = $2`, fallbackID, keyID)
				require.NoError(t, err)
			}
			_, err := tx.ExecContext(ctx, string(migrationSQL))
			require.NoError(t, err)
			requireColumn(t, tx, "api_keys", "fallback_group_id", "bigint", 0, true)
			requireIndex(t, tx, "api_keys", "idx_api_keys_fallback_group_id")

			var got sql.NullInt64
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT fallback_group_id FROM api_keys WHERE id = $1`, keyID).Scan(&got))
			if existingColumn {
				require.Equal(t, sql.NullInt64{Int64: fallbackID, Valid: true}, got, "upgrade preserves configured fallback")
			} else {
				require.False(t, got.Valid, "existing keys do not gain a fallback automatically")
			}

			_, err = tx.ExecContext(ctx, `UPDATE api_keys SET fallback_group_id = $1 WHERE id = $2`, fallbackID, keyID)
			require.NoError(t, err)
			for range 2 {
				_, err = tx.ExecContext(ctx, string(migrationSQL))
				require.NoError(t, err)
				require.NoError(t, tx.QueryRowContext(ctx, `SELECT fallback_group_id FROM api_keys WHERE id = $1`, keyID).Scan(&got))
				require.Equal(t, sql.NullInt64{Int64: fallbackID, Valid: true}, got, "replay preserves configured fallback")
			}

			_, err = tx.ExecContext(ctx, `DELETE FROM groups WHERE id = $1`, fallbackID)
			require.NoError(t, err)
			var gotPrimary int64
			require.NoError(t, tx.QueryRowContext(ctx, `SELECT group_id, fallback_group_id FROM api_keys WHERE id = $1`, keyID).Scan(&gotPrimary, &got))
			require.Equal(t, primaryID, gotPrimary, "deleting the fallback retains the key and its primary group")
			require.False(t, got.Valid)

			_, err = tx.ExecContext(ctx, `UPDATE api_keys SET fallback_group_id = $1 WHERE id = $2`, fallbackID, keyID)
			var constraintErr *pq.Error
			require.ErrorAs(t, err, &constraintErr)
			require.Equal(t, pq.ErrorCode("23503"), constraintErr.Code)
			require.Equal(t, "api_keys_fallback_group_id_fkey", constraintErr.Constraint)
		})
	}
}
