//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"io/fs"
	"net/url"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Validate the deployed fork's migration order: the newly merged official 249
// must run even though custom migrations through 328 already exist.
func TestMCPOAuthUpgradeFromCustom328PreservesAccountAndKey(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	name := "mcp_upgrade_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	_, err := integrationDB.ExecContext(ctx, "CREATE DATABASE "+name)
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = integrationDB.ExecContext(context.Background(), "DROP DATABASE "+name+" WITH (FORCE)")
	})
	dsn, err := url.Parse(integrationDatabaseDSN)
	require.NoError(t, err)
	dsn.Path = "/" + name
	db, err := sql.Open("postgres", dsn.String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	files, err := fs.Glob(migrations.FS, "*.sql")
	require.NoError(t, err)
	prior := fstest.MapFS{}
	for _, file := range files {
		if file == "249_mcp_oauth.sql" || file == "329_retire_custom_routing_identity_and_rate_limit.sql" {
			continue
		}
		data, readErr := fs.ReadFile(migrations.FS, file)
		require.NoError(t, readErr)
		prior[file] = &fstest.MapFile{Data: data}
	}
	require.NoError(t, applyMigrationsFS(ctx, db, prior))
	var userID, accountID int64
	require.NoError(t, db.QueryRowContext(ctx,
		"INSERT INTO users (email,password_hash) VALUES ('upgrade@example.test','test-hash') RETURNING id").Scan(&userID))
	const extra = `{"codex_fingerprint_mode":"single_machine_multi_window","codex_fingerprint_seed":"stable","proxy_ids":[7,8],"upstream_billing_rate_limit":0,"upstream_billing_probe_enabled":false,"model_rate_limits":{"gpt-5.5":"keep"}}`
	require.NoError(t, db.QueryRowContext(ctx,
		"INSERT INTO accounts (name,platform,type,credentials,extra,concurrency,schedulable,status) VALUES ('upgrade','openai','oauth',$1::jsonb,$2::jsonb,12,false,'quality_paused') RETURNING id",
		`{"access_token":"preserved"}`, extra).Scan(&accountID))
	stored := service.APIKeyStoredValue("sk-test-upgrade-credential-preserved")
	_, err = db.ExecContext(ctx, "INSERT INTO api_keys (user_id,key,name) VALUES ($1,$2,'upgrade')", userID, stored)
	require.NoError(t, err)
	var before, after, extraBefore string
	const accountState = "SELECT (to_jsonb(a) - 'extra' - 'updated_at')::text FROM accounts a WHERE id=$1"
	require.NoError(t, db.QueryRowContext(ctx, accountState, accountID).Scan(&before))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT extra::text FROM accounts WHERE id=$1", accountID).Scan(&extraBefore))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, db.QueryRowContext(ctx, accountState, accountID).Scan(&after))
	require.JSONEq(t, before, after)
	var currentExtra, currentKey string
	require.NoError(t, db.QueryRowContext(ctx, "SELECT extra::text FROM accounts WHERE id=$1", accountID).Scan(&currentExtra))
	var expectedExtra map[string]any
	require.NoError(t, json.Unmarshal([]byte(extraBefore), &expectedExtra))
	expectedExtra["codex_fingerprint_mode"] = "off"
	delete(expectedExtra, "upstream_billing_rate_limit")
	expectedJSON, err := json.Marshal(expectedExtra)
	require.NoError(t, err)
	require.JSONEq(t, string(expectedJSON), currentExtra)
	require.NoError(t, db.QueryRowContext(ctx, "SELECT key FROM api_keys WHERE user_id=$1", userID).Scan(&currentKey))
	require.Equal(t, stored, currentKey)
	var grants int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM mcp_oauth_grants").Scan(&grants))
	require.Zero(t, grants)
	var count int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM schema_migrations").Scan(&count))
	require.Len(t, files, count)
	var eventsBefore, eventsAfter int
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox").Scan(&eventsBefore))
	require.NoError(t, ApplyMigrations(ctx, db))
	require.NoError(t, db.QueryRowContext(ctx, "SELECT count(*) FROM scheduler_outbox").Scan(&eventsAfter))
	require.Equal(t, eventsBefore, eventsAfter)
}
