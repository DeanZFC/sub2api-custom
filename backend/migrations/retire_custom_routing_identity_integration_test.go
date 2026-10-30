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

func TestRetireCustomRoutingIdentityAndRateLimitMigration(t *testing.T) {
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
	schema := "retire_identity_" + uuid.New().String()[:8]
	_, err = conn.ExecContext(ctx, "CREATE SCHEMA "+schema)
	require.NoError(t, err)
	t.Cleanup(func() { _, _ = conn.ExecContext(context.Background(), "DROP SCHEMA "+schema+" CASCADE") })
	_, err = conn.ExecContext(ctx, "SET search_path TO "+schema)
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, `
 CREATE TABLE accounts (id BIGINT PRIMARY KEY, extra JSONB,
   credentials JSONB DEFAULT '{"access_token":"preserved"}', proxy_id BIGINT DEFAULT 7,
   concurrency INTEGER DEFAULT 12, status TEXT DEFAULT 'active', schedulable BOOLEAN DEFAULT TRUE,
   temp_unschedulable_reason TEXT DEFAULT 'provider:block', updated_at TIMESTAMPTZ DEFAULT '2026-01-01');
 CREATE TABLE scheduler_outbox (event_type TEXT, account_id BIGINT);
 INSERT INTO accounts (id, extra) VALUES
 (1, '{"codex_fingerprint_mode":"single_machine_multi_window","codex_fingerprint_seed":"stable","enable_tls_fingerprint":true,"proxy_ids":[7,8],"upstream_billing_rate_limit":0,"upstream_billing_probe_enabled":false,"upstream_billing_rate_sync_enabled":false,"model_rate_limits":{"gpt-5.5":"cooldown"}}'),
 (2, '{"codex_fingerprint_mode":"off"}'), (3, '[]'), (4, '42'), (5, 'null'), (6, NULL),
 (7, '{"codex_fingerprint_mode":"device","codex_fingerprint_seed":"device-seed","upstream_billing_rate_limit":1.5,"upstream_billing_probe_enabled":true,"upstream_billing_rate_sync_enabled":true,"upstream_billing_probe":{"status":"ok","data":{"resolved_rate_multiplier":2}}}'),
 (8, '{"codex_fingerprint_mode":"session"}'), (9, '{"codex_fingerprint_mode":"full"}'),
 (10, '{"codex_fingerprint_mode":" single_machine_multi_window "}'),
 (11, '{"upstream_billing_rate_limit":null}');
 UPDATE accounts SET status='quality_paused', schedulable=false WHERE id=1;
 `)
	require.NoError(t, err)
	const preserved = `SELECT jsonb_agg(to_jsonb(a) - 'extra' - 'updated_at' ORDER BY id)::text FROM accounts a`
	var before, after string
	require.NoError(t, conn.QueryRowContext(ctx, preserved).Scan(&before))
	migration, err := FS.ReadFile("329_retire_custom_routing_identity_and_rate_limit.sql")
	require.NoError(t, err)
	_, err = conn.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, preserved).Scan(&after))
	require.JSONEq(t, before, after)
	var extraJSON []byte
	require.NoError(t, conn.QueryRowContext(ctx, "SELECT extra FROM accounts WHERE id=1").Scan(&extraJSON))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(extraJSON, &extra))
	require.Equal(t, "off", extra["codex_fingerprint_mode"])
	require.Equal(t, "stable", extra["codex_fingerprint_seed"])
	require.Equal(t, true, extra["enable_tls_fingerprint"])
	require.Equal(t, []any{float64(7), float64(8)}, extra["proxy_ids"])
	require.Equal(t, false, extra["upstream_billing_probe_enabled"])
	require.NotContains(t, extra, "upstream_billing_rate_limit")
	for query, want := range map[string]int{
		"SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed' AND account_id IN (1,7,10,11)": 4,
		"SELECT count(*) FROM accounts WHERE id IN (2,3,4,5,6,8,9) AND updated_at='2026-01-01'":                  7,
		`SELECT count(*) FROM accounts WHERE id=7 AND extra->>'codex_fingerprint_mode'='device' AND extra->>'codex_fingerprint_seed'='device-seed' AND extra @> '{"upstream_billing_probe_enabled":true,"upstream_billing_rate_sync_enabled":true,"upstream_billing_probe":{"status":"ok"}}'`: 1,
		"SELECT count(*) FROM accounts WHERE id=10 AND extra->>'codex_fingerprint_mode'='off'": 1,
		"SELECT count(*) FROM accounts WHERE extra ? 'upstream_billing_rate_limit'":            0,
	} {
		var got int
		require.NoError(t, conn.QueryRowContext(ctx, query).Scan(&got))
		require.Equal(t, want, got, query)
	}
	const snapshot = `SELECT jsonb_build_object('accounts', (SELECT jsonb_agg(to_jsonb(a) ORDER BY id) FROM accounts a), 'outbox', (SELECT jsonb_agg(to_jsonb(o) ORDER BY account_id) FROM scheduler_outbox o))::text`
	require.NoError(t, conn.QueryRowContext(ctx, snapshot).Scan(&before))
	_, err = conn.ExecContext(ctx, string(migration))
	require.NoError(t, err)
	require.NoError(t, conn.QueryRowContext(ctx, snapshot).Scan(&after))
	require.JSONEq(t, before, after, "repeat migration must not mutate data or enqueue duplicate events")
}
