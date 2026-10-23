//go:build integration

package repository

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/stretchr/testify/require"
)

func TestAPIKeySecretMigrationPreservesCredentialsAndInvalidatesDigest(t *testing.T) {
	ctx := context.Background()
	tx := testTx(t)
	_, err := tx.ExecContext(ctx, "ALTER TABLE api_keys DROP CONSTRAINT api_keys_digest_only")
	require.NoError(t, err)
	var userID, keyID int64
	require.NoError(t, tx.QueryRowContext(ctx, "INSERT INTO users (email,password_hash) VALUES ('key-migration@test.com','test') RETURNING id").Scan(&userID))
	const raw = "sk-migration-keep-123456789"
	require.NoError(t, tx.QueryRowContext(ctx, "INSERT INTO api_keys (user_id,key,name,quota,quota_used) VALUES ($1,$2,'keep',100,17) RETURNING id", userID, raw).Scan(&keyID))
	_, err = tx.ExecContext(ctx, "INSERT INTO idempotency_records (scope,idempotency_key_hash,request_fingerprint,status,response_body,expires_at) VALUES ('user.api_keys.create','migration-test','test','succeeded',$1,NOW()+interval '1 day')", raw)
	require.NoError(t, err)
	sql, err := dbmigrations.FS.ReadFile("300_api_key_digest_only.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(sql))
		require.NoError(t, err)
		var stored, cacheDigest, body string
		var quota float64
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT key,api_key_cache_digest(key),quota_used FROM api_keys WHERE id=$1", keyID).Scan(&stored, &cacheDigest, &quota))
		require.Equal(t, service.APIKeyStoredValue(raw), stored)
		require.Equal(t, service.APIKeyCacheDigest(raw), cacheDigest)
		require.Equal(t, 17.0, quota)
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT response_body FROM idempotency_records WHERE idempotency_key_hash='migration-test'").Scan(&body))
		require.NotContains(t, body, raw)
	}
	_, err = tx.ExecContext(ctx, "DELETE FROM auth_cache_invalidation_outbox")
	require.NoError(t, err)
	const rotated = "sk-migration-new-987654321"
	_, err = tx.ExecContext(ctx, "UPDATE api_keys SET key=$1 WHERE id=$2", service.APIKeyStoredValue(rotated), keyID)
	require.NoError(t, err)
	for _, secret := range []string{raw, rotated} {
		var count int
		require.NoError(t, tx.QueryRowContext(ctx, "SELECT count(*) FROM auth_cache_invalidation_outbox WHERE cache_key=$1", service.APIKeyCacheDigest(secret)).Scan(&count))
		require.Equal(t, 1, count)
	}
	_, err = tx.ExecContext(ctx, "UPDATE api_keys SET key='plaintext-is-forbidden' WHERE id=$1", keyID)
	require.Error(t, err)
}
