//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	_ "github.com/Wei-Shaw/sub2api/ent/runtime"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAccountQualityStateConcurrentFullUpdateIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("account_quality_state_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	defer func() {
		_, err := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	}()
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	query.Set("application_name", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(8)
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	defer client.Close()
	repo := newAccountRepositoryWithSQL(client, db, nil)
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(`CREATE TABLE accounts (
id BIGINT PRIMARY KEY, created_at TIMESTAMPTZ DEFAULT NOW(), updated_at TIMESTAMPTZ DEFAULT NOW(), deleted_at TIMESTAMPTZ,
name TEXT NOT NULL DEFAULT 'test', notes TEXT, platform TEXT NOT NULL DEFAULT 'openai', type TEXT NOT NULL DEFAULT 'oauth',
credentials JSONB NOT NULL DEFAULT '{}', extra JSONB NOT NULL DEFAULT '{}', proxy_id BIGINT, proxy_fallback_origin_id BIGINT,
concurrency INTEGER NOT NULL DEFAULT 3, load_factor INTEGER, priority INTEGER NOT NULL DEFAULT 50, rate_multiplier NUMERIC NOT NULL DEFAULT 1,
status TEXT NOT NULL DEFAULT 'active', error_message TEXT, last_used_at TIMESTAMPTZ, expires_at TIMESTAMPTZ,
auto_pause_on_expired BOOLEAN NOT NULL DEFAULT TRUE, schedulable BOOLEAN NOT NULL DEFAULT TRUE,
rate_limited_at TIMESTAMPTZ, rate_limit_reset_at TIMESTAMPTZ, overload_until TIMESTAMPTZ, temp_unschedulable_until TIMESTAMPTZ,
temp_unschedulable_reason TEXT, session_window_start TIMESTAMPTZ, session_window_end TIMESTAMPTZ, session_window_status TEXT,
parent_account_id BIGINT, quota_dimension TEXT NOT NULL DEFAULT 'global');
CREATE TABLE scheduler_outbox (id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_account_quality_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO accounts (id) VALUES (41);`)
	newAccount := func() *service.Account {
		return &service.Account{ID: 41, Name: "edited", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
			Status: service.StatusActive, Schedulable: true, Concurrency: 3, Priority: 50, AutoPauseOnExpired: true,
			Credentials: map[string]any{"access_token": "refreshed-token"}, Extra: map[string]any{"setting": "edited"}}
	}
	assertState := func(status string, schedulable bool, reason string) {
		t.Helper()
		var actualStatus, actualReason, actualToken string
		var actualSchedulable bool
		err := db.QueryRowContext(ctx, `SELECT status,schedulable,COALESCE(extra->>'quality_protection_reason',''),credentials->>'access_token' FROM accounts WHERE id=41`).Scan(&actualStatus, &actualSchedulable, &actualReason, &actualToken)
		require.NoError(t, err)
		require.Equal(t, status, actualStatus)
		require.Equal(t, schedulable, actualSchedulable)
		require.Equal(t, reason, actualReason)
		require.Equal(t, "refreshed-token", actualToken, "the unrelated credential update should still complete")
	}
	for _, tt := range []struct {
		name        string
		mutation    string
		status      string
		schedulable bool
		reason      string
	}{
		{"quality protection", `UPDATE accounts SET status='quality_paused',extra='{"quality_protection_reason":"new failure"}' WHERE id=41`, "quality_paused", true, "new failure"},
		{"manual scheduling pause", `UPDATE accounts SET schedulable=FALSE WHERE id=41`, "active", false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			exec(`UPDATE accounts SET status='active',schedulable=TRUE,extra='{}' WHERE id=41`)
			staleAccount := newAccount()
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			defer tx.Rollback()
			_, err = tx.ExecContext(ctx, tt.mutation)
			require.NoError(t, err)
			completed := make(chan error, 1)
			go func() { completed <- repo.Update(ctx, staleAccount) }()
			require.Eventually(t, func() bool {
				var waiting bool
				err := admin.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE application_name=$1 AND wait_event_type='Lock')`, schema).Scan(&waiting)
				return err == nil && waiting
			}, 5*time.Second, 10*time.Millisecond, "the full update must wait for the concurrent account mutation")
			require.NoError(t, tx.Commit())
			require.NoError(t, <-completed)
			assertState(tt.status, tt.schedulable, tt.reason)
			require.Equal(t, tt.status, staleAccount.Status)
			require.Equal(t, tt.schedulable, staleAccount.Schedulable)
		})
	}
	t.Run("explicit release applies once and cannot resume manual scheduling", func(t *testing.T) {
		exec(`UPDATE accounts SET status='quality_paused',schedulable=FALSE,extra='{"quality_protection_reason":"old failure"}' WHERE id=41`)
		account := newAccount()
		account.StatusChanged = true
		require.NoError(t, repo.Update(ctx, account))
		assertState("active", false, "")
		require.False(t, account.StatusChanged, "explicit intent must be consumed after saving")
		exec(`UPDATE accounts SET status='quality_paused',extra='{"quality_protection_reason":"later failure"}' WHERE id=41`)
		require.NoError(t, repo.Update(ctx, account))
		assertState("quality_paused", false, "later failure")
	})
	t.Run("stale protected snapshot cannot reverse subsequent manual status", func(t *testing.T) {
		exec(`UPDATE accounts SET status='disabled',schedulable=TRUE,extra='{}' WHERE id=41`)
		account := newAccount()
		account.Status = service.StatusQualityPaused
		account.Extra["quality_protection_reason"] = "old failure"
		require.NoError(t, repo.Update(ctx, account))
		assertState("disabled", true, "")
	})
}
