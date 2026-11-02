//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestLeaseMultiInstanceClaimAndFencing(t *testing.T) {
	ctx := context.Background()
	schema := fmt.Sprintf("lease_test_%d", time.Now().UnixNano())
	_, err := integrationDB.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.ExecContext(ctx, "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, err)
	})
	parsed, err := url.Parse(integrationDatabaseDSN)
	require.NoError(t, err)
	query := parsed.Query()
	query.Set("search_path", schema)
	parsed.RawQuery = query.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	_, err = db.Exec(`CREATE TABLE scheduled_test_plans(id BIGINT PRIMARY KEY, enabled BOOLEAN NOT NULL, next_run_at TIMESTAMPTZ, last_run_at TIMESTAMPTZ, cron_expression TEXT, schedule_config JSONB, updated_at TIMESTAMPTZ); INSERT INTO scheduled_test_plans VALUES(1,true,clock_timestamp()-interval '1 minute',NULL,'*/5 * * * *',NULL,NOW());`)
	require.NoError(t, err)
	migration, err := migrations.FS.ReadFile("331_scheduled_test_execution_leases.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(migration))
		require.NoError(t, err)
	}
	_, err = db.Exec("CREATE TABLE scheduled_test_results(id BIGINT PRIMARY KEY,plan_id BIGINT,account_id BIGINT,test_definition_id BIGINT,model_id TEXT,status TEXT,error_message TEXT,finished_at TIMESTAMPTZ); INSERT INTO scheduled_test_results(id,plan_id,account_id,test_definition_id,model_id,status) VALUES(1,1,1,1,'m','running')")
	require.NoError(t, err)
	a, b := &scheduledTestPlanRepository{db: db}, &scheduledTestPlanRepository{db: db}
	var due time.Time
	require.NoError(t, db.QueryRow(`SELECT next_run_at FROM scheduled_test_plans WHERE id=1`).Scan(&due))
	plan := &service.ScheduledTestPlan{ID: 1, Enabled: true, NextRunAt: &due, CronExpression: "*/5 * * * *"}
	var wins atomic.Int32
	var wg sync.WaitGroup
	for i := range 12 {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := a.ClaimExecution(ctx, plan, "plan", fmt.Sprint(i), true)
			require.NoError(t, err)
			if ok {
				wins.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 1, wins.Load())
	ok, err := b.ClaimExecution(ctx, plan, "retry:1:1:m", "retry", false)
	require.NoError(t, err)
	require.False(t, ok)
	var owner string
	require.NoError(t, db.QueryRow(`SELECT owner FROM scheduled_test_execution_leases`).Scan(&owner))
	require.NoError(t, b.ReleaseExecution(ctx, 1, "plan", "wrong-owner"))
	ok, err = a.RenewExecution(ctx, 1, "plan", owner)
	require.NoError(t, err)
	require.True(t, ok)
	_, err = db.Exec(`UPDATE scheduled_test_execution_leases SET expires_at=clock_timestamp()-interval '1 second'`)
	require.NoError(t, err)
	ok, err = a.RenewExecution(ctx, 1, "plan", owner)
	require.NoError(t, err)
	require.False(t, ok, "expired ownership cannot resurrect")
	ok, err = b.ClaimExecution(ctx, plan, "plan", "replacement", true)
	require.NoError(t, err)
	require.True(t, ok)
	var abandonedStatus string
	require.NoError(t, db.QueryRow("SELECT status FROM scheduled_test_results WHERE id=1").Scan(&abandonedStatus))
	require.Equal(t, "failed", abandonedStatus)
	oldResult := &service.ScheduledTestResult{PlanID: 1, ExecutionLeaseScope: "plan", ExecutionLeaseOwner: owner}
	_, err = execScheduledTestResultUnderLease(ctx, db, oldResult, "UPDATE scheduled_test_results SET status='success' WHERE id=1")
	require.ErrorContains(t, err, "lease lost")
	newResult := *oldResult
	newResult.ExecutionLeaseOwner = "replacement"
	_, err = execScheduledTestResultUnderLease(ctx, db, &newResult, "UPDATE scheduled_test_results SET status='success' WHERE id=1")
	require.NoError(t, err)
	require.NoError(t, a.ReleaseExecution(ctx, 1, "plan", owner))
	plan.ExecutionLeaseOwner, plan.ExecutionLeaseScope = owner, "plan"
	require.ErrorContains(t, a.UpdateAfterRun(ctx, plan, time.Now(), time.Now().Add(time.Hour)), "lease lost")
	var stillDue time.Time
	require.NoError(t, db.QueryRow(`SELECT next_run_at FROM scheduled_test_plans WHERE id=1`).Scan(&stillDue))
	require.Equal(t, due, stillDue)
	plan.ExecutionLeaseOwner = "replacement"
	require.NoError(t, b.UpdateAfterRun(ctx, plan, time.Now(), time.Now().Add(time.Hour)))
	require.NoError(t, b.ReleaseExecution(ctx, 1, "plan", "replacement"))
	ok, err = a.ClaimExecution(ctx, plan, "plan", "stale-tick", true)
	require.NoError(t, err)
	require.False(t, ok, "stale ListDue result must not replay completed cron tick")
	ok, err = a.ClaimExecution(ctx, plan, "retry:1:1:m", "r1", false)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = b.ClaimExecution(ctx, plan, "retry:1:1:m", "r2", false)
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = b.ClaimExecution(ctx, plan, "retry:2:1:m", "r3", false)
	require.NoError(t, err)
	require.True(t, ok)
	ok, err = a.ClaimExecution(ctx, plan, "plan", "manual", false)
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, a.ReleaseExecution(ctx, 1, "retry:1:1:m", "r1"))
	require.NoError(t, b.ReleaseExecution(ctx, 1, "retry:2:1:m", "r3"))
	ok, err = a.ClaimExecution(ctx, plan, "plan", "manual", false)
	require.NoError(t, err)
	require.True(t, ok)
}
