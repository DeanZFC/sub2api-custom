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

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestActiveTestAdmissionIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("active_admission_%d", time.Now().UnixNano())
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
	parsed.RawQuery = query.Encode()
	openDB := func() *sql.DB {
		db, err := sql.Open("postgres", parsed.String())
		require.NoError(t, err)
		db.SetMaxOpenConns(8)
		t.Cleanup(func() { _ = db.Close() })
		return db
	}

	db := openDB()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec("CREATE TABLE accounts(id bigint PRIMARY KEY,status text NOT NULL DEFAULT 'active',schedulable boolean NOT NULL DEFAULT true,deleted_at timestamptz,extra jsonb NOT NULL DEFAULT '{}'); CREATE TABLE scheduled_test_protection_states(account_id bigint,model_key text NOT NULL DEFAULT '',blocked boolean NOT NULL DEFAULT true); CREATE TABLE scheduled_test_combination_states(account_id bigint,model_key text NOT NULL DEFAULT '',account_wide boolean NOT NULL DEFAULT false,blocked boolean NOT NULL DEFAULT true); CREATE TABLE scheduled_test_pass_rate_states(account_id bigint,model_key text NOT NULL DEFAULT '',blocked boolean NOT NULL DEFAULT true); INSERT INTO accounts(id)VALUES(41)")
	repo := &accountRepository{sql: db}
	check := func(model string, want bool, held bool) {
		t.Helper()
		allowed, err := repo.CheckAccountAdmission(context.WithValue(ctx, ctxkey.RequestedPublicModel, model), 41)
		if held {
			require.ErrorIs(t, err, service.ErrAccountQualityHeld)
		} else {
			require.NoError(t, err)
		}
		require.Equal(t, want, allowed)
	}
	reset := func() {
		exec("TRUNCATE scheduled_test_protection_states,scheduled_test_combination_states,scheduled_test_pass_rate_states; UPDATE accounts SET status='active',schedulable=true,deleted_at=NULL,extra='{}'")
	}
	t.Run("live manual admission", func(t *testing.T) {
		reset()
		check("a", true, false)
		for _, status := range []string{"disabled", "error"} {
			exec("UPDATE accounts SET status=$1", status)
			check("a", false, false)
		}
		exec("UPDATE accounts SET status='active',schedulable=false")
		check("a", false, false)
		exec("UPDATE accounts SET schedulable=true,deleted_at=now()")
		check("a", false, false)
		allowed, err := repo.CheckAccountAdmission(ctx, 999)
		require.NoError(t, err)
		require.False(t, allowed)
	})
	for _, table := range []string{"scheduled_test_protection_states", "scheduled_test_pass_rate_states", "scheduled_test_combination_states"} {
		t.Run(table, func(t *testing.T) {
			reset()
			exec("INSERT INTO " + table + "(account_id,model_key)VALUES(41,'public-a')")
			check("public-a", false, true)
			check("", false, true)
			check("public-b", true, false)
			exec("UPDATE " + table + " SET blocked=false")
			check("public-a", true, false)
		})
	}
	t.Run("public alias ownership", func(t *testing.T) {
		reset()
		exec("INSERT INTO scheduled_test_pass_rate_states(account_id,model_key)VALUES(41,'public-a')")
		request := context.WithValue(context.WithValue(ctx, ctxkey.Model, "upstream-alias"), ctxkey.RequestedPublicModel, "public-a")
		allowed, err := repo.CheckAccountAdmission(request, 41)
		require.ErrorIs(t, err, service.ErrAccountQualityHeld)
		require.False(t, allowed)
	})
	t.Run("account wide and projected holds", func(t *testing.T) {
		reset()
		exec("UPDATE accounts SET status='quality_paused'")
		check("unmonitored", false, true)
		reset()
		exec("UPDATE accounts SET extra=$1::jsonb", `{"active_test_all_models_paused":true}`)
		check("unmonitored", false, true)
		reset()
		exec("INSERT INTO scheduled_test_combination_states(account_id,model_key,account_wide)VALUES(41,'public-a',true)")
		check("unmonitored", false, true)
	})
	t.Run("manual stop remains authoritative", func(t *testing.T) {
		reset()
		exec("INSERT INTO scheduled_test_protection_states(account_id,model_key)VALUES(41,'public-a'); UPDATE accounts SET schedulable=false")
		check("public-a", false, false)
		check("public-b", false, false)
	})
}
