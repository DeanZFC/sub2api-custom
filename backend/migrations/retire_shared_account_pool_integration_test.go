//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRetireSharedAccountPoolMigration(t *testing.T) {
	for _, legacy := range []string{"none", "original", "latest"} {
		t.Run(legacy, func(t *testing.T) {
			dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
			if dsn == "" {
				t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
			}
			db, err := sql.Open("postgres", dsn)
			require.NoError(t, err)
			t.Cleanup(func() { _ = db.Close() })
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			conn, err := db.Conn(ctx)
			require.NoError(t, err)
			t.Cleanup(func() { _ = conn.Close() })
			schema := "retire_shared_" + uuid.New().String()[:8]
			_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
			_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, `
CREATE TABLE accounts (id BIGINT PRIMARY KEY, status TEXT DEFAULT 'active', schedulable BOOLEAN DEFAULT TRUE, deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE groups (id BIGINT PRIMARY KEY, name TEXT, status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE proxies (id BIGINT PRIMARY KEY, status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE users (id BIGINT PRIMARY KEY, balance NUMERIC NOT NULL);
CREATE TABLE api_keys (id BIGINT PRIMARY KEY, key TEXT NOT NULL, user_id BIGINT DEFAULT 1, group_id BIGINT,
 status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT '2026-01-01',
 ip_whitelist JSONB, ip_blacklist JSONB, expires_at TIMESTAMPTZ);
CREATE TABLE account_groups (account_id BIGINT, group_id BIGINT);
CREATE TABLE scheduler_outbox (event_type TEXT, account_id BIGINT, group_id BIGINT, payload JSONB);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE usage_logs (id BIGINT, account_id BIGINT, api_key_id BIGINT, actual_cost NUMERIC);
INSERT INTO accounts (id) VALUES (1), (2), (3);
INSERT INTO groups (id, name) VALUES (1, 'normal'), (2, 'renamed internal'), (3, 'shared-openai');
INSERT INTO account_groups VALUES (2, 1), (2, 2), (3, 2);
INSERT INTO proxies (id) VALUES (1), (2);
INSERT INTO users VALUES (1, 47.12);
INSERT INTO api_keys (id, key, group_id) VALUES (1, 'sk-normal', 1), (2, 'sk-shared-original', 1),
 (3, 'custom-shared-key', 1), (4, 'changed-legacy-key', 1), (5, 'internal-group-key', 2);
INSERT INTO usage_logs VALUES (1, 2, 2, 1.23);
INSERT INTO settings VALUES ('shared_pool_enabled', 'true'), ('shared_pool_fee_rate_percent', '10'), ('openai_codex_ticket_enabled', 'true');
`)
			require.NoError(t, err)
			// Exercise the real API-key invalidation trigger used on upgrade.
			auth, err := FS.ReadFile("184_auth_cache_invalidation_outbox.sql")
			require.NoError(t, err)
			end := strings.Index(string(auth), "CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation")
			require.Positive(t, end)
			_, err = conn.ExecContext(ctx, string(auth[:end]))
			require.NoError(t, err)
			if legacy != "none" {
				_, err = conn.ExecContext(ctx, `
ALTER TABLE accounts ADD account_scope TEXT DEFAULT 'system';
ALTER TABLE groups ADD is_shared_pool BOOLEAN DEFAULT FALSE;
ALTER TABLE proxies ADD owner_user_id BIGINT;
UPDATE accounts SET account_scope='shared' WHERE id=2;
UPDATE groups SET is_shared_pool=TRUE WHERE id=2;
UPDATE proxies SET owner_user_id=1 WHERE id=2;
CREATE TABLE shared_account_listings (id BIGINT PRIMARY KEY, account_id BIGINT, status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE shared_api_keys (id BIGINT PRIMARY KEY, key TEXT, status TEXT DEFAULT 'active', deleted_at TIMESTAMPTZ, updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE shared_account_wallets (user_id BIGINT, pending_amount NUMERIC, available_amount NUMERIC, frozen_amount NUMERIC);
CREATE TABLE shared_account_usage_ledger (id BIGINT, listing_id BIGINT, owner_amount NUMERIC);
INSERT INTO shared_account_listings (id, account_id) VALUES (1, 2), (2, 3);
INSERT INTO shared_api_keys (id, key) VALUES (1, 'custom-shared-key'), (2, 'original-legacy-key');
INSERT INTO shared_account_wallets VALUES (1, 3.5, 7.2, 1.1);
INSERT INTO shared_account_usage_ledger VALUES (1, 1, 2.3);
`)
				require.NoError(t, err)
			}
			if legacy == "latest" {
				_, err = conn.ExecContext(ctx, `
ALTER TABLE shared_account_listings ADD listed BOOLEAN DEFAULT TRUE;
ALTER TABLE shared_api_keys ADD legacy_api_key_id BIGINT;
UPDATE shared_api_keys SET legacy_api_key_id=4 WHERE id=2;
`)
				require.NoError(t, err)
			}
			migration, err := FS.ReadFile("255_retire_shared_account_pool.sql")
			require.NoError(t, err)
			_, err = conn.ExecContext(ctx, string(migration))
			require.NoError(t, err)
			var count int
			countRows := func(query string, expected int) {
				t.Helper()
				require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&count))
				require.Equal(t, expected, count, query)
			}
			countRows("SELECT count(*) FROM accounts WHERE id=1 AND status='active' AND schedulable AND deleted_at IS NULL AND updated_at='2026-01-01'", 1)
			countRows("SELECT count(*) FROM groups WHERE id IN (1,3) AND status='active' AND deleted_at IS NULL", 2)
			countRows("SELECT count(*) FROM proxies WHERE id=1 AND status='active' AND deleted_at IS NULL", 1)
			countRows("SELECT count(*) FROM api_keys WHERE id=1 AND status='active' AND deleted_at IS NULL", 1)
			countRows("SELECT count(*) FROM api_keys WHERE id=2 AND status='disabled' AND deleted_at IS NOT NULL", 1)
			countRows("SELECT count(*) FROM users WHERE balance=47.12", 1)
			countRows("SELECT count(*) FROM usage_logs WHERE account_id=2 AND api_key_id=2 AND actual_cost=1.23", 1)
			countRows("SELECT count(*) FROM settings", 1)
			countRows("SELECT count(*) FROM settings WHERE key='openai_codex_ticket_enabled' AND value='true'", 1)
			if legacy == "none" {
				countRows("SELECT count(*) FROM accounts WHERE status='active' AND deleted_at IS NULL", 3)
				countRows("SELECT count(*) FROM scheduler_outbox", 0)
				countRows("SELECT count(*) FROM auth_cache_invalidation_outbox", 1)
			} else {
				countRows("SELECT count(*) FROM accounts WHERE id IN (2,3) AND status='disabled' AND NOT schedulable AND deleted_at IS NOT NULL", 2)
				countRows("SELECT count(*) FROM groups WHERE id=2 AND status='disabled' AND deleted_at IS NOT NULL", 1)
				countRows("SELECT count(*) FROM proxies WHERE id=2 AND status='disabled' AND deleted_at IS NOT NULL", 1)
				countRows("SELECT count(*) FROM shared_account_listings WHERE status='deleted' AND deleted_at IS NOT NULL", 2)
				countRows("SELECT count(*) FROM shared_api_keys WHERE status='disabled' AND deleted_at IS NOT NULL", 2)
				countRows("SELECT count(*) FROM shared_account_wallets WHERE pending_amount=3.5 AND available_amount=7.2 AND frozen_amount=1.1", 1)
				countRows("SELECT count(*) FROM shared_account_usage_ledger WHERE owner_amount=2.3", 1)
				countRows("SELECT count(*) FROM scheduler_outbox", 3)
				countRows("SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed' AND account_id=2 AND payload @> '{\"group_ids\":[1,2]}'", 1)
				countRows("SELECT count(*) FROM scheduler_outbox WHERE event_type='group_changed' AND group_id=2", 1)
				if legacy == "latest" {
					countRows("SELECT count(*) FROM shared_account_listings WHERE listed", 0)
					countRows("SELECT count(*) FROM api_keys WHERE id IN (2,3,4,5) AND status='disabled' AND deleted_at IS NOT NULL", 4)
					countRows("SELECT count(*) FROM auth_cache_invalidation_outbox", 4)
				} else {
					countRows("SELECT count(*) FROM api_keys WHERE id IN (2,3,5) AND status='disabled' AND deleted_at IS NOT NULL", 3)
					countRows("SELECT count(*) FROM auth_cache_invalidation_outbox", 3)
				}
			}
			var before, after string
			const snapshot = `SELECT jsonb_build_object('accounts',(SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
 'groups',(SELECT jsonb_agg(to_jsonb(g) ORDER BY id) FROM groups g), 'proxies',(SELECT jsonb_agg(to_jsonb(p) ORDER BY id) FROM proxies p),
 'keys',(SELECT jsonb_agg(to_jsonb(k) ORDER BY id) FROM api_keys k), 'scheduler',(SELECT count(*) FROM scheduler_outbox),
 'auth',(SELECT count(*) FROM auth_cache_invalidation_outbox))::text`
			require.NoError(t, conn.QueryRowContext(ctx, snapshot).Scan(&before))
			_, err = conn.ExecContext(ctx, string(migration))
			require.NoError(t, err)
			require.NoError(t, conn.QueryRowContext(ctx, snapshot).Scan(&after))
			require.JSONEq(t, before, after, "repeated migration must not change resources or enqueue events again")
		})
	}
}
