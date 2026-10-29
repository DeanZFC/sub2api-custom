//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/lib/pq"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func qualityOperationsDB(t *testing.T) (context.Context, *sql.DB, *qualityOperationsRepository, *sql.DB) {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		dsn = integrationDatabaseDSN
	}
	require.NotEmpty(t, dsn)
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("quality_operations_%d", time.Now().UnixNano())
	_, err = admin.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, err)
		admin.Close()
	})
	_, err = db.Exec("CREATE TABLE accounts(id bigint PRIMARY KEY,name text,status text DEFAULT 'active',schedulable boolean DEFAULT true,deleted_at timestamptz,extra jsonb DEFAULT '{}',updated_at timestamptz DEFAULT now()); INSERT INTO accounts(id,name) VALUES(62,'A'),(63,'B'); CREATE TABLE scheduled_test_plans(id bigint PRIMARY KEY,latest_run_id text,updated_at timestamptz NOT NULL DEFAULT now()); INSERT INTO scheduled_test_plans(id,latest_run_id)VALUES(1,'run'); CREATE TABLE scheduled_test_pass_rate_states(plan_id bigint,account_id bigint,test_definition_id bigint,model_key text,blocked boolean DEFAULT false)")
	require.NoError(t, err)
	for _, name := range []string{"294_quality_operations.sql", "309_quality_recovery_budget_evidence.sql"} {
		raw, err := os.ReadFile("../../migrations/" + name)
		require.NoError(t, err)
		_, err = db.Exec(string(raw))
		require.NoError(t, err)
	}
	return ctx, db, &qualityOperationsRepository{db: db}, db
}
func TestQualityOperationsBudgetConcurrentIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	snapshot, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	c := snapshot.Config
	c.BudgetEnabled = true
	c.DailyRequests = 7
	c.AccountDailyRequests = 3
	c.DailyTokens = 0
	c.AccountDailyTokens = 0
	c.TokenReservation = 1024
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	stale := c
	stale.Version--
	require.ErrorIs(t, r.SaveOperationsConfig(ctx, &stale), service.ErrQualityOperationsConflict)
	var wg sync.WaitGroup
	var mu sync.Mutex
	var admitted []*service.QualityProbeReservation
	for i := 0; i < 40; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			res, e := r.ReserveQualityProbe(ctx, int64(62+i%2), 100, false, "")
			if e != nil {
				var d *service.QualityDeferredError
				require.ErrorAs(t, e, &d)
				return
			}
			mu.Lock()
			admitted = append(admitted, res)
			mu.Unlock()
		}(i)
	}
	wg.Wait()
	require.Len(t, admitted, 6)
	snapshot, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 6, snapshot.Usage[0].Requests)
	require.EqualValues(t, 6144, snapshot.Usage[0].Tokens)
	actual := int64(12)
	outcome := service.QualityProbeOutcome{Tokens: &actual, Kind: "healthy"}
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[0], outcome))
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[0], outcome))
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[1], service.QualityProbeOutcome{Kind: "network"}))
	snapshot, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 5132, snapshot.Usage[0].Tokens)
	require.EqualValues(t, 5, snapshot.Usage[0].UnknownRequests)
	// Today's ledger starts independently, while late settlement charges its
	// original reservation day. No result rows are used to calculate budgets.
	_, err = db.Exec(`UPDATE quality_probe_attempts SET day=day-1; UPDATE quality_budget_days SET day=day-1`)
	require.NoError(t, err)
	next, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	require.NotNil(t, next)
	require.NoError(t, r.SettleQualityProbe(ctx, admitted[2], outcome))
	snapshot, err = r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Usage[0].Requests)
	require.EqualValues(t, 1024, snapshot.Usage[0].Tokens)
}

func TestQualityOperationsTokenOverspendAndUnknownIntegration(t *testing.T) {
	ctx, _, r, _ := qualityOperationsDB(t)
	c := service.DefaultQualityOperationsConfig()
	c.BudgetEnabled = true
	c.TokenReservation = 1024
	c.DailyTokens = 2048
	c.AccountDailyTokens = 0
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	first, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	second, err := r.ReserveQualityProbe(ctx, 63, 100, false, "")
	require.NoError(t, err)
	_, err = r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.Error(t, err)
	require.NoError(t, r.SettleQualityProbe(ctx, first, service.QualityProbeOutcome{Kind: "server"}))
	_, err = r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.Error(t, err, "missing usage must retain reservation")
	actual := int64(4096)
	require.NoError(t, r.SettleQualityProbe(ctx, second, service.QualityProbeOutcome{Tokens: &actual, Kind: "healthy"}))
	_, err = r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.Error(t, err, "reported usage above reserve stops later requests")
	s, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 5120, s.Usage[0].Tokens)
}
func TestQualityOperationsHealthyFrequencyFencingIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	c := service.DefaultQualityOperationsConfig()
	c.DynamicEnabled = true
	c.HealthyRounds = 2
	c.HealthyBaseSeconds = 60
	c.HealthyMaxSeconds = 240
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	plan := &service.ScheduledTestPlan{ID: 1}
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM scheduled_test_plans WHERE id=1`).Scan(&plan.UpdatedAt))
	id := int64(62)
	result := &service.ScheduledTestResult{AccountID: &id, RunID: "run", StartedAt: time.Now().Add(-time.Minute)}
	for i := 0; i < 2; i++ {
		result.StartedAt = result.StartedAt.Add(time.Second)
		require.NoError(t, r.RecordQualityFrequency(ctx, plan, result, "policy", true))
	}
	next, err := r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.NotNil(t, next)
	next, err = r.QualityFrequencyDue(ctx, 1, id, "edited-policy")
	require.NoError(t, err)
	require.Nil(t, next)
	stale := *result
	stale.RunID = "old"
	stale.StartedAt = time.Now()
	require.NoError(t, r.RecordQualityFrequency(ctx, plan, &stale, "policy", false))
	next, err = r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.NotNil(t, next)
}

func TestQualityOperationsCapabilityHoldPreventsHealthyFrequencyIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	c := service.DefaultQualityOperationsConfig()
	c.DynamicEnabled, c.HealthyRounds, c.HealthyBaseSeconds, c.HealthyMaxSeconds = true, 2, 60, 240
	require.NoError(t, r.SaveOperationsConfig(ctx, &c))
	plan := &service.ScheduledTestPlan{ID: 1}
	require.NoError(t, db.QueryRow(`SELECT updated_at FROM scheduled_test_plans WHERE id=1`).Scan(&plan.UpdatedAt))
	id := int64(62)
	result := &service.ScheduledTestResult{AccountID: &id, RunID: "run", StartedAt: time.Now().Add(-time.Minute)}
	for range 2 {
		result.StartedAt = result.StartedAt.Add(time.Second)
		require.NoError(t, r.RecordQualityFrequency(ctx, plan, result, "policy", true))
	}
	next, err := r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.NotNil(t, next)
	_, err = db.Exec(`INSERT INTO scheduled_test_pass_rate_states(plan_id,account_id,test_definition_id,model_key,blocked) VALUES(1,62,2,'model',true)`)
	require.NoError(t, err)
	next, err = r.QualityFrequencyDue(ctx, 1, id, "policy")
	require.NoError(t, err)
	require.Nil(t, next, "capability recovery must not wait on a previous healthy streak")
	result.StartedAt = time.Now()
	require.NoError(t, r.RecordQualityFrequency(ctx, plan, result, "policy", true))
	var streak int
	require.NoError(t, db.QueryRow(`SELECT streak FROM quality_healthy_frequency WHERE plan_id=1 AND account_id=62`).Scan(&streak))
	require.Zero(t, streak, "a passing round does not claim health while another independent capability hold persists")
}

func TestQualityOperationsRetentionPreservesCurrentLedgerIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	old, err := r.ReserveQualityProbe(ctx, 62, 100, false, "")
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE quality_probe_attempts SET started_at=now()-INTERVAL '31 days' WHERE id=$1`, old.ID)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO quality_budget_days(day,account_id,requests,tokens,unknown_requests) VALUES((now() AT TIME ZONE 'UTC')::date-91,0,1,1024,1)`)
	require.NoError(t, err)
	require.NoError(t, r.PruneQualityOperations(ctx))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM quality_probe_attempts`).Scan(&count))
	require.Zero(t, count)
	snapshot, err := r.OperationsSnapshot(ctx)
	require.NoError(t, err)
	require.EqualValues(t, 1, snapshot.Usage[0].Requests)
	require.EqualValues(t, 32768, snapshot.Usage[0].Tokens)
}
