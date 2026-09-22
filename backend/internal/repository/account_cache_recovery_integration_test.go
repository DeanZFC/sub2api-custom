//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestAccountCacheRecoveryAdmissionIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("cache_admission_%d", time.Now().UnixNano())
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
	db, otherDB := openDB(), openDB()
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,deleted_at TIMESTAMPTZ);
		CREATE TABLE scheduled_test_protection_states(plan_id BIGINT,account_id BIGINT,test_definition_id BIGINT,blocked BOOLEAN NOT NULL DEFAULT TRUE,
		 recovery_phase TEXT NOT NULL DEFAULT 'trial',recovery_trial_started_at TIMESTAMPTZ DEFAULT clock_timestamp()-INTERVAL '1 minute',
		 recovery_trial_ends_at TIMESTAMPTZ DEFAULT clock_timestamp()+INTERVAL '1 hour',recovery_trial_requests BIGINT NOT NULL DEFAULT 0,rule_config JSONB NOT NULL,
		 PRIMARY KEY(plan_id,account_id,test_definition_id));
		CREATE TABLE scheduled_test_combination_states(plan_id BIGINT,account_id BIGINT,blocked BOOLEAN NOT NULL DEFAULT TRUE,PRIMARY KEY(plan_id,account_id));
		INSERT INTO accounts(id) VALUES(41);`)
	repos := []*accountRepository{{sql: db}, {sql: otherDB}}
	check := func(want bool) {
		t.Helper()
		allowed, err := repos[0].AcquireCacheRecoveryRequest(ctx, 41)
		require.NoError(t, err)
		require.Equal(t, want, allowed)
	}
	reset := func() {
		exec(`TRUNCATE scheduled_test_protection_states,scheduled_test_combination_states; UPDATE accounts SET status='active',schedulable=TRUE,deleted_at=NULL`)
	}
	hold := func(planID, maxRequests int64) {
		exec(`INSERT INTO scheduled_test_protection_states(plan_id,account_id,test_definition_id,rule_config)
		 VALUES($1,41,1,jsonb_build_object('recovery',jsonb_build_object('enabled',true,'max_requests',$2::bigint)))`, planID, maxRequests)
	}

	t.Run("live account status defeats stale scheduler snapshots", func(t *testing.T) {
		reset()
		check(true)
		for _, status := range []string{"quality_paused", "disabled", "error"} {
			exec(`UPDATE accounts SET status=$1`, status)
			check(false)
		}
		exec(`UPDATE accounts SET status='active',schedulable=FALSE`)
		check(false)
		exec(`UPDATE accounts SET schedulable=TRUE,deleted_at=clock_timestamp()`)
		check(false)
		allowed, err := repos[0].AcquireCacheRecoveryRequest(ctx, 999)
		require.NoError(t, err)
		require.False(t, allowed)
	})
	t.Run("all holds must allow the same request", func(t *testing.T) {
		reset()
		hold(1, 3)
		hold(2, 3)
		exec(`UPDATE scheduled_test_protection_states SET recovery_phase='cooldown' WHERE plan_id=2`)
		check(false)
		var used int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT SUM(recovery_trial_requests) FROM scheduled_test_protection_states`).Scan(&used))
		require.Zero(t, used, "one hold cannot consume or release another hold")
		exec(`UPDATE scheduled_test_protection_states SET recovery_phase='trial',recovery_trial_ends_at=clock_timestamp()-INTERVAL '1 second' WHERE plan_id=2`)
		check(false)
		exec(`UPDATE scheduled_test_protection_states SET recovery_trial_ends_at=clock_timestamp()+INTERVAL '1 hour',recovery_trial_started_at=clock_timestamp()+INTERVAL '1 minute' WHERE plan_id=2`)
		check(false)
		exec(`UPDATE scheduled_test_protection_states SET blocked=FALSE WHERE plan_id=2`)
		check(true)
	})
	t.Run("combination hold overrides an active cache trial", func(t *testing.T) {
		reset()
		hold(1, 3)
		exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id) VALUES(2,41)`)
		check(false)
		var used int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT recovery_trial_requests FROM scheduled_test_protection_states WHERE plan_id=1`).Scan(&used))
		require.Zero(t, used, "combination rejection must not consume the cache trial budget")
		exec(`UPDATE scheduled_test_combination_states SET blocked=FALSE`)
		check(true)
	})
	t.Run("two instances share the smallest atomic trial budget", func(t *testing.T) {
		reset()
		hold(1, 7)
		hold(2, 11)
		var admitted atomic.Int64
		var wg sync.WaitGroup
		errs := make(chan error, 64)
		for i := range 64 {
			wg.Add(1)
			go func(index int) {
				defer wg.Done()
				allowed, err := repos[index%2].AcquireCacheRecoveryRequest(ctx, 41)
				if err != nil {
					errs <- err
					return
				}
				if allowed {
					admitted.Add(1)
				}
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		require.Equal(t, int64(7), admitted.Load())
		var min, max int64
		require.NoError(t, db.QueryRowContext(ctx, `SELECT MIN(recovery_trial_requests),MAX(recovery_trial_requests) FROM scheduled_test_protection_states`).Scan(&min, &max))
		require.Equal(t, int64(7), min)
		require.Equal(t, int64(7), max)
		check(false)
	})
	t.Run("invalid trial configurations fail closed", func(t *testing.T) {
		reset()
		hold(1, 2)
		for _, raw := range []string{`{}`, `{"recovery":{"enabled":false,"max_requests":2}}`, `{"recovery":{"enabled":true,"max_requests":0}}`} {
			exec(`UPDATE scheduled_test_protection_states SET rule_config=$1::jsonb`, raw)
			check(false)
		}
		exec(`UPDATE scheduled_test_protection_states SET rule_config='{"recovery":{"enabled":true,"max_requests":"invalid"}}'`)
		allowed, err := repos[0].AcquireCacheRecoveryRequest(ctx, 41)
		require.Error(t, err)
		require.False(t, allowed)
	})
}
