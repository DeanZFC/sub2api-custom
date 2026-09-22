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

	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

// This fixture deliberately includes real binding constraints and group
// metadata: outcome actions modify memberships, not only an account status.
func TestScheduledTestActionsIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("quality_actions_%d", time.Now().UnixNano())
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
	db.SetMaxOpenConns(12)
	currentT := t
	exec := func(statement string, args ...any) {
		currentT.Helper()
		_, err := db.ExecContext(ctx, statement, args...)
		require.NoError(currentT, err)
	}
	exec(`CREATE TABLE users (id BIGINT PRIMARY KEY);
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,extra JSONB NOT NULL DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',deleted_at TIMESTAMPTZ,is_exclusive BOOLEAN NOT NULL DEFAULT FALSE,subscription_type TEXT NOT NULL DEFAULT 'standard');
CREATE TABLE account_groups(account_id BIGINT NOT NULL REFERENCES accounts(id),group_id BIGINT NOT NULL REFERENCES groups(id),priority INTEGER NOT NULL DEFAULT 50,created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),PRIMARY KEY(account_id,group_id));
CREATE TABLE user_allowed_groups(user_id BIGINT,group_id BIGINT);
CREATE TABLE user_subscriptions(user_id BIGINT,group_id BIGINT,deleted_at TIMESTAMPTZ,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ);
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_actions_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO users SELECT generate_series(1,20);
INSERT INTO accounts(id,name) VALUES(62,'Secret account name'),(63,'Other account');
INSERT INTO groups(id,name) VALUES(8,'Source and lower tier'),(9,'Unrelated membership'),(10,'Upper tier one'),(11,'Upper tier two'),(12,'Independent upper tier'),(13,'Independent lower tier');`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql", "249_scheduled_test_result_reasoning_effort.sql",
		"250_allow_group_account_scheduled_test_targets.sql", "251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql", "256_scheduled_test_multiple_definitions.sql", "257_scheduled_test_hourly_statistics.sql",
		"258_scheduled_test_protection.sql", "259_scheduled_test_outcome_actions.sql",
		"260_scheduled_test_model_check.sql", "261_scheduled_test_admin_review.sql", "261_scheduled_test_admin_review.sql",
		"262_scheduled_test_execution_snapshot.sql",
		"264_scheduled_test_cache_recovery.sql", "265_scheduled_test_generic_policy.sql", "268_scheduled_test_combination_states.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		exec(string(raw))
	}
	var candyID, pelicanID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='candy'`).Scan(&candyID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='pelican'`).Scan(&pelicanID))
	plans := &scheduledTestPlanRepository{db: db}
	repo := &scheduledTestResultRepository{db: db}
	reset := func(t *testing.T) {
		currentT = t
		exec(`TRUNCATE scheduled_test_plans,scheduled_test_results,scheduled_test_plan_definitions,scheduled_test_protection_states,scheduled_test_managed_accounts,scheduled_test_votes,scheduler_outbox RESTART IDENTITY CASCADE`)
		exec(`UPDATE accounts SET platform='openai',status='active',schedulable=TRUE,deleted_at=NULL,extra='{}';
UPDATE groups SET platform='openai',status='active',deleted_at=NULL,is_exclusive=FALSE;
TRUNCATE user_allowed_groups,user_subscriptions,account_groups;
INSERT INTO account_groups(account_id,group_id,priority) VALUES(62,8,71),(62,9,72),(63,8,73);`)
	}
	t.Run("public switch preserves round and decisions", func(t *testing.T) { reset(t); testScheduledTestPublicVotingSwitch(t, ctx, db, plans, repo, pelicanID) })
	t.Run("generic priorities public review and administrator override", func(t *testing.T) {
		reset(t)
		testScheduledTestGenericPublicVotes(t, ctx, db, plans, repo, candyID, pelicanID)
	})
	t.Run("generic multi group snapshot and required rules", func(t *testing.T) {
		reset(t)
		testScheduledTestGenericPolicy(t, ctx, db, plans, repo, candyID, pelicanID)
	})
	t.Run("generic routing conflicts", func(t *testing.T) { reset(t); testScheduledTestGenericConflicts(t, ctx, db, plans, candyID) })
	t.Run("combined condition lifecycle and retry", func(t *testing.T) {
		reset(t)
		testScheduledTestCombinedLifecycle(t, ctx, db, plans, repo, candyID, pelicanID)
	})
	t.Run("combined votes and administrator verdicts", func(t *testing.T) {
		reset(t)
		testScheduledTestCombinedVotes(t, ctx, db, plans, repo, candyID, pelicanID)
	})
	t.Run("combined ownership and hold cleanup", func(t *testing.T) {
		reset(t)
		testScheduledTestCombinedConflictAndCleanup(t, ctx, db, plans, repo, candyID, pelicanID)
	})
	t.Run("combined equal priority merges actions", func(t *testing.T) {
		reset(t)
		testScheduledTestCombinedMergedActions(t, ctx, db, plans, repo, candyID, pelicanID)
	})
}
