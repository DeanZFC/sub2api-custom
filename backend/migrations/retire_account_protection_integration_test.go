//go:build integration

package migrations

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
	_ "github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestRetireAccountProtectionMigration(t *testing.T) {
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
	schema := "cleanup_" + uuid.New().String()[:8]
	_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `
CREATE TABLE accounts (
 id BIGINT PRIMARY KEY, platform TEXT NOT NULL DEFAULT 'openai', type TEXT NOT NULL DEFAULT 'oauth',
 extra JSONB NOT NULL DEFAULT '{}', credentials JSONB NOT NULL DEFAULT '{}', concurrency INTEGER NOT NULL DEFAULT 16,
 temp_unschedulable_reason TEXT, temp_unschedulable_until TIMESTAMPTZ,
 rate_limit_reset_at TIMESTAMPTZ DEFAULT '2099-01-01', overload_until TIMESTAMPTZ DEFAULT '2099-01-02',
 updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE scheduler_outbox (event_type TEXT NOT NULL, account_id BIGINT);
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
INSERT INTO settings VALUES ('account_health_settings','{}'), ('openai_codex_ticket_enabled','true');
INSERT INTO accounts (id, extra, credentials, concurrency, temp_unschedulable_reason, temp_unschedulable_until) VALUES
 (1, '{"anti_degradation":true,"anti_degrade":{"enabled":true,"mode":"legacy","applied_concurrency":16,"prev":{"concurrency":0,"enable_tls_fingerprint":true,"tls_fingerprint_profile_id":7}},"enable_tls_fingerprint":true,"tls_fingerprint_builtin":"nodejs24","codex_fingerprint_seed":"11111111-1111-4111-8111-111111111111","account_protection_policy":{"enabled":true},"codex_turn_ticket:gpt-6-astra":{"state":"keep"},"proxy_ids":[7,8]}', '{"access_token":"keep","prism_cookie":"remove","prism_cookie_configured":true}',16,'health:auto:test','2099-02-01'),
 (2, '{"anti_degradation":true,"anti_degrade":{"enabled":true,"mode":"mode1","applied_concurrency":16,"prev":{"concurrency":0}},"request_integrity_mode":"enforce"}', '{}',3,'provider:block','2099-02-01'),
 (3, '{"anti_degradation":true,"anti_degrade":{"enabled":true,"mode":"generic","applied_concurrency":16,"prev":{"concurrency":0}},"codex_fingerprint_mode":"off","enable_tls_fingerprint":true}', '{}',16,NULL,NULL),
 (4, '{"prism":{"enabled":true},"codex_fingerprint_mode":"off"}', '{"refresh_token":"keep","prism_cookie":"remove"}',5,NULL,NULL),
 (5, '{"codex_fingerprint_mode":"device"}', '{"access_token":"untouched"}',9,'manual:block','2099-02-01'),
 (6, '{"codex_fingerprint_mode":"account_device"}', '{}',4,NULL,NULL),
 (7, '{"anti_degradation":true,"anti_degrade":{"mode":"legacy","prev":{"concurrency":"malformed"}},"tls_fingerprint_builtin":"nodejs24"}', '{}',5,NULL,NULL),
 (8, '{"anti_degrade":{"enabled":true,"mode":"legacy","applied_concurrency":16,"prev":{"concurrency":-1,"enable_tls_fingerprint":true,"tls_fingerprint_profile_id":42,"codex_fingerprint_mode":null}},"codex_fingerprint_mode":"session","tls_fingerprint_builtin":"nodejs24"}', '{}',16,NULL,NULL);
UPDATE accounts SET platform='anthropic' WHERE id=8;
`)
	require.NoError(t, err)
	migration, err := FS.ReadFile("254_retire_prism_and_account_protection.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(migration))
	require.NoError(t, err)

	type state struct {
		ID          int64          `json:"id"`
		Extra       map[string]any `json:"extra"`
		Credentials map[string]any `json:"credentials"`
		Concurrency int            `json:"concurrency"`
		Reason      *string        `json:"temp_unschedulable_reason"`
	}
	var snapshot []byte
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a").Scan(&snapshot))
	var accounts []state
	require.NoError(t, json.Unmarshal(snapshot, &accounts))
	first := accounts[0]
	require.Equal(t, "single_machine_multi_window", first.Extra["codex_fingerprint_mode"])
	require.Equal(t, "11111111-1111-4111-8111-111111111111", first.Extra["codex_fingerprint_seed"])
	require.Equal(t, float64(7), first.Extra["tls_fingerprint_profile_id"])
	require.Equal(t, []any{float64(7), float64(8)}, first.Extra["proxy_ids"])
	require.Contains(t, first.Extra, "codex_turn_ticket:gpt-6-astra")
	require.Equal(t, 0, first.Concurrency)
	require.Nil(t, first.Reason)
	require.Equal(t, map[string]any{"access_token": "keep"}, first.Credentials)
	require.Equal(t, 3, accounts[1].Concurrency, "keep manually edited concurrency")
	require.Equal(t, "provider:block", *accounts[1].Reason)
	require.Equal(t, "off", accounts[2].Extra["codex_fingerprint_mode"], "generic protection never enables identity rewriting")
	require.Equal(t, true, accounts[2].Extra["enable_tls_fingerprint"])
	require.Equal(t, 0, accounts[2].Concurrency)
	require.Equal(t, map[string]any{"refresh_token": "keep"}, accounts[3].Credentials)
	require.Equal(t, "device", accounts[5].Extra["codex_fingerprint_mode"])
	// Exact legacy seed for account 6: switching the alias must not change its device identity.
	require.Equal(t, "e30296a7-e2f0-4497-8357-a6433ac83d56", accounts[5].Extra["codex_fingerprint_seed"])
	seed, err := uuid.Parse(accounts[5].Extra["codex_fingerprint_seed"].(string))
	require.NoError(t, err)
	require.Equal(t, uuid.Version(4), seed.Version())
	require.Equal(t, 5, accounts[6].Concurrency, "invalid snapshot cannot reset concurrency")
	require.Equal(t, -1, accounts[7].Concurrency)
	require.Equal(t, float64(42), accounts[7].Extra["tls_fingerprint_profile_id"])
	require.NotContains(t, accounts[7].Extra, "codex_fingerprint_mode", "Anthropic restores its original settings")
	for _, a := range accounts {
		for _, key := range []string{"prism", "anti_degrade", "anti_degradation", "account_protection_policy", "request_integrity_mode", "tls_fingerprint_builtin"} {
			require.NotContains(t, a.Extra, key)
		}
	}
	var intact int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT count(*) FROM accounts WHERE rate_limit_reset_at='2099-01-01' AND overload_until='2099-01-02'").Scan(&intact))
	require.Equal(t, 8, intact)
	var untouched string
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT updated_at::date::text FROM accounts WHERE id=5").Scan(&untouched))
	require.Equal(t, "2026-01-01", untouched)
	var outboxCount int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox").Scan(&outboxCount))
	require.Equal(t, 7, outboxCount)
	var settingCount int
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT count(*) FROM settings WHERE key='openai_codex_ticket_enabled' AND value='true'").Scan(&settingCount))
	require.Equal(t, 1, settingCount)

	_, err = conn.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	var repeated []byte
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a").Scan(&repeated))
	require.JSONEq(t, string(snapshot), string(repeated), "second execution must be a no-op")
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox").Scan(&intact))
	require.Equal(t, outboxCount, intact)
}
