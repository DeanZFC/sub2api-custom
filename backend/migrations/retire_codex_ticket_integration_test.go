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

func TestRetireCodexTicketMigration(t *testing.T) {
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
	schema := "retire_ticket_" + uuid.New().String()[:8]
	_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `
CREATE TABLE accounts (id BIGINT PRIMARY KEY, extra JSONB, credentials JSONB DEFAULT '{"access_token":"keep"}',
 proxy_id BIGINT DEFAULT 7, concurrency INTEGER DEFAULT 12, status TEXT DEFAULT 'active', schedulable BOOLEAN DEFAULT TRUE,
 temp_unschedulable_reason TEXT DEFAULT 'provider:block', updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
CREATE TABLE settings (key TEXT PRIMARY KEY, value TEXT);
CREATE TABLE scheduler_outbox (event_type TEXT, account_id BIGINT);
INSERT INTO settings VALUES ('openai_codex_ticket_enabled','true'), ('openai_codex_ticket_harvest_proxy_ids','[7]'),
 ('codex_retry_settings','keep'), ('openai_codex_ticketish','keep');
INSERT INTO accounts (id, extra) VALUES
 (1, '{"codex_turn_ticket:gpt-5.5":{"state":"obsolete"},"codex_turn_cookies":{"__cflb":"obsolete"},"codex_harvest_proxy_url":"obsolete","codex_ticket_enabled":true,"codex_ticket_fail_closed":true,"codex_ticket_harvest_proxy_ids":[7],"codex_fingerprint_mode":"single_machine_multi_window","codex_fingerprint_seed":"stable","enable_tls_fingerprint":true,"proxy_ids":[7,8],"upstream_billing_rate_limit":1.5,"model_rate_limits":{"gpt-5.5":"cooldown"},"codex_usage_updated_at":"keep"}'),
 (2, '{"codex_fingerprint_mode":"off"}'), (3, '[]'), (4, '42'), (5, 'null'), (6, NULL),
 (7, '{"codex_ticket_enabled":false}');
`)
	require.NoError(t, err)
	const preserved = `SELECT jsonb_agg(to_jsonb(a) - 'extra' - 'updated_at' ORDER BY id)::text FROM accounts a`
	var before, after string
	require.NoError(t, conn.QueryRowContext(ctx, preserved).Scan(&before))
	migration, err := FS.ReadFile("269_retire_codex_ticket_harvesting.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, preserved).Scan(&after))
	require.JSONEq(t, before, after)
	var extraJSON []byte
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT extra FROM accounts WHERE id=1").Scan(&extraJSON))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(extraJSON, &extra))
	require.Len(t, extra, 7)
	require.Equal(t, "single_machine_multi_window", extra["codex_fingerprint_mode"])
	require.Equal(t, "stable", extra["codex_fingerprint_seed"])
	require.Equal(t, []any{float64(7), float64(8)}, extra["proxy_ids"])
	require.Equal(t, true, extra["enable_tls_fingerprint"])
	require.Equal(t, 1.5, extra["upstream_billing_rate_limit"])
	require.Equal(t, map[string]any{"gpt-5.5": "cooldown"}, extra["model_rate_limits"])
	require.Equal(t, "keep", extra["codex_usage_updated_at"])
	for query, want := range map[string]int{
		"SELECT count(*) FROM settings WHERE value='keep'":                                                 2,
		"SELECT count(*) FROM settings":                                                                    2,
		"SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed' AND account_id IN (1,7)": 2,
		"SELECT count(*) FROM accounts WHERE id BETWEEN 2 AND 6 AND updated_at='2026-01-01'":               5,
		"SELECT count(*) FROM accounts WHERE id=7 AND extra='{}'":                                          1,
	} {
		var got int
		require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&got))
		require.Equal(t, want, got, query)
	}
	const snapshot = `SELECT jsonb_build_object('accounts', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a),
 'settings', (SELECT jsonb_agg(to_jsonb(s) ORDER BY key) FROM settings s),
 'outbox', (SELECT jsonb_agg(to_jsonb(o) ORDER BY account_id) FROM scheduler_outbox o))::text`
	require.NoError(t, conn.QueryRowContext(ctx, snapshot).Scan(&before))
	_, err = conn.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, snapshot).Scan(&after))
	require.JSONEq(t, before, after, "reapplying the migration must not change data or emit more events")
}
