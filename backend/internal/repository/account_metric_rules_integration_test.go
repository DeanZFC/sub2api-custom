//go:build integration

package repository

import (
	"context"
	"database/sql"
	"fmt"
	"net/url"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func metricRulesDB(t *testing.T) *sql.DB {
	t.Helper()
	schema := fmt.Sprintf("metric_rules_%d", time.Now().UnixNano())
	_, err := integrationDB.Exec("CREATE SCHEMA " + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, err := integrationDB.Exec("DROP SCHEMA " + pq.QuoteIdentifier(schema) + " CASCADE")
		require.NoError(t, err)
	})
	u, err := url.Parse(integrationDatabaseDSN)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	_, err = db.Exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,deleted_at TIMESTAMPTZ,schedulable BOOLEAN DEFAULT false);
 CREATE TABLE settings(key TEXT PRIMARY KEY,value TEXT);
 CREATE TABLE usage_logs(id BIGSERIAL,account_id BIGINT DEFAULT 1,api_key_id BIGINT DEFAULT 1,request_id TEXT,created_at TIMESTAMPTZ,request_type INT DEFAULT 2,stream BOOLEAN DEFAULT true,openai_ws_mode BOOLEAN DEFAULT false,
 actual_cost NUMERIC DEFAULT 0,input_tokens BIGINT DEFAULT 10,output_tokens BIGINT DEFAULT 10,cache_read_tokens BIGINT DEFAULT 90,cache_creation_tokens BIGINT DEFAULT 0,image_input_tokens BIGINT DEFAULT 0,first_token_ms BIGINT DEFAULT 100,duration_ms BIGINT DEFAULT 1000);
 CREATE TABLE ops_error_logs(id BIGSERIAL,account_id BIGINT DEFAULT 1,api_key_id BIGINT DEFAULT 1,request_id TEXT,client_request_id TEXT,created_at TIMESTAMPTZ,request_type INT DEFAULT 2,is_count_tokens BOOLEAN DEFAULT false,status_code INT DEFAULT 503);
 INSERT INTO accounts(id)VALUES(1),(2);`)
	require.NoError(t, err)
	for _, file := range []string{"339_account_performance_monitor.sql", "341_account_metric_rules.sql"} {
		raw, err := os.ReadFile("../../migrations/" + file)
		require.NoError(t, err)
		_, err = db.Exec(string(raw))
		require.NoError(t, err)
	}
	return db
}
func metricConfig() service.PerformanceConfig {
	c := service.DefaultPerformanceConfig()
	c.Enabled = true
	c.MinSamples = 3
	c.RecoverySamples = 2
	c.CooldownMinutes = 1
	minimum := 90.0
	c.SuccessRate.Pause.Min = &minimum
	return c
}
func readMetric(t *testing.T, r *accountPerformanceRepository, now time.Time) service.AccountPerformance {
	t.Helper()
	m, err := r.Read(context.Background(), []int64{1}, now)
	require.NoError(t, err)
	return m[1]
}
func TestMetricRulesPostgresPauseTrialRecoverAndIndependentOwnership(t *testing.T) {
	db := metricRulesDB(t)
	ctx := context.Background()
	r := &accountPerformanceRepository{db: db}
	gate := &accountRepository{sql: db}
	base := time.Now().Add(-2 * time.Minute)
	require.NoError(t, r.Save(ctx, 1, metricConfig(), 0, 42))
	_, err := db.Exec(`INSERT INTO ops_error_logs(created_at,request_id) SELECT $1,'failure-'||n FROM generate_series(1,3)n`, base.Add(-time.Second))
	require.NoError(t, err)
	require.NoError(t, r.Evaluate(ctx, 1, base))
	m := readMetric(t, r, base)
	require.True(t, m.Paused)
	require.Equal(t, "success_rate", m.Reason)
	paused, err := gate.PerformancePaused(ctx, []int64{1})
	require.NoError(t, err)
	require.True(t, paused[1])
	ids, err := r.EnabledIDs(ctx)
	require.NoError(t, err)
	require.Contains(t, ids, int64(1), "paused accounts remain evaluated for recovery")
	allowed, _, err := gate.AcquirePerformanceTrial(ctx, 1)
	require.NoError(t, err)
	require.False(t, allowed)
	require.ErrorIs(t, r.Release(ctx, 1, 0, 1, 42), service.ErrPerformanceConflict)
	now := time.Now()
	require.NoError(t, r.Evaluate(ctx, 1, now))
	m = readMetric(t, r, now)
	require.NotNil(t, m.TrialStartedAt)
	require.Nil(t, m.Metrics.SuccessRate, "old successes/failures are excluded from recovery")
	// Independent repository instances compete for a single persisted trial slot.
	var wg sync.WaitGroup
	results := make(chan bool, 20)
	errs := make(chan error, 20)
	for range 20 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other := &accountRepository{sql: db}
			ok, _, err := other.AcquirePerformanceTrial(ctx, 1)
			results <- ok
			errs <- err
		}()
	}
	wg.Wait()
	close(results)
	close(errs)
	count := 0
	for ok := range results {
		if ok {
			count++
		}
	}
	for err := range errs {
		require.NoError(t, err)
	}
	require.Equal(t, 1, count)
	paused, err = gate.PerformancePaused(ctx, []int64{1})
	require.NoError(t, err)
	require.True(t, paused[1], "rate gate excludes trial account until next slot")
	require.NoError(t, r.Evaluate(ctx, 1, now.Add(time.Second)))
	require.True(t, readMetric(t, r, now.Add(time.Second)).Paused, "no samples cannot recover")
	_, err = db.Exec(`INSERT INTO usage_logs(created_at,request_id) SELECT $1,'trial-'||n FROM generate_series(1,2)n`, now.Add(time.Millisecond))
	require.NoError(t, err)
	require.NoError(t, r.Evaluate(ctx, 1, now.Add(2*time.Second)))
	m = readMetric(t, r, now.Add(2*time.Second))
	require.False(t, m.Paused)
	require.Nil(t, m.TrialStartedAt)
	require.True(t, m.Config.Enabled)
	require.NoError(t, r.Evaluate(ctx, 1, now.Add(3*time.Second)))
	require.False(t, readMetric(t, r, now.Add(3*time.Second)).Paused, "old failures do not immediately re-pause a recovered account")
	var schedulable bool
	require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=1`).Scan(&schedulable))
	require.False(t, schedulable, "automatic recovery must preserve manual disable")
	events, err := r.Events(ctx, 1)
	require.NoError(t, err)
	actions := []string{}
	for _, event := range events {
		actions = append(actions, event.Action)
	}
	require.Equal(t, []string{"recover", "trial", "pause", "configure"}, actions)
}
func TestMetricRulesPostgresTimeoutBudgetRefundAndDisable(t *testing.T) {
	db := metricRulesDB(t)
	ctx := context.Background()
	r := &accountPerformanceRepository{db: db}
	gate := &accountRepository{sql: db}
	require.NoError(t, r.Save(ctx, 1, metricConfig(), 0, 0))
	now := time.Now()
	_, err := db.Exec(`UPDATE account_performance_monitors SET paused=true,paused_at=$1,pause_generation=1 WHERE account_id=1`, now.Add(-2*time.Minute))
	require.NoError(t, err)
	require.NoError(t, r.Evaluate(ctx, 1, now))
	allowed, cancel, err := gate.AcquirePerformanceTrial(ctx, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	require.NotNil(t, cancel)
	cancel()
	cancel()
	m := readMetric(t, r, now.Add(time.Second))
	require.Zero(t, m.TrialRequests, "failed downstream admission refunds once")
	_, err = db.Exec(`UPDATE account_performance_monitors SET next_trial_at=NOW()-INTERVAL '1 second',trial_requests=5 WHERE account_id=1`)
	require.NoError(t, err)
	allowed, oldRefund, err := gate.AcquirePerformanceTrial(ctx, 1)
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = db.Exec(`UPDATE account_performance_monitors SET next_trial_at=NOW()-INTERVAL '1 second' WHERE account_id=1`)
	require.NoError(t, err)
	allowed, _, err = gate.AcquirePerformanceTrial(ctx, 1)
	require.NoError(t, err)
	require.False(t, allowed, "budget is enforced even after rate interval")
	require.NoError(t, r.Evaluate(ctx, 1, now.Add(11*time.Minute)))
	m = readMetric(t, r, now.Add(11*time.Minute))
	require.True(t, m.Paused)
	require.Nil(t, m.TrialStartedAt)
	require.Equal(t, "trial_timeout", m.Reason)
	require.EqualValues(t, 2, m.PauseGeneration)
	// A late callback from the previous generation must not consume a new trial.
	_, err = db.Exec(`UPDATE account_performance_monitors SET trial_started_at=NOW(),trial_requests=2 WHERE account_id=1`)
	require.NoError(t, err)
	oldRefund()
	require.EqualValues(t, 2, readMetric(t, r, time.Now()).TrialRequests)
	cfg := metricConfig()
	cfg.Enabled = false
	require.NoError(t, r.Save(ctx, 1, cfg, m.Revision, 42))
	m = readMetric(t, r, time.Now())
	require.False(t, m.Paused)
	require.Nil(t, m.TrialStartedAt)
	require.False(t, m.Config.Enabled)
	require.ErrorIs(t, r.Save(ctx, 1, cfg, m.Revision-1, 42), service.ErrPerformanceConflict)
}
func TestMetricRulesPostgresMigrationRetiresOnlyOwnedPause(t *testing.T) {
	db := metricRulesDB(t)
	_, err := db.Exec(`INSERT INTO account_performance_monitors(account_id,config,paused,revision) VALUES(1,'{"enabled":true,"upstream_error":{"max":10}}',true,4)`)
	require.NoError(t, err)
	raw, err := os.ReadFile("../../migrations/341_account_metric_rules.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	r := &accountPerformanceRepository{db: db}
	m := readMetric(t, r, time.Now())
	require.False(t, m.Paused)
	require.False(t, m.Config.Enabled)
	require.Equal(t, 2, m.Config.Version)
	require.EqualValues(t, 5, m.Revision)
	_, err = db.Exec(string(raw))
	require.NoError(t, err)
	require.EqualValues(t, 5, readMetric(t, r, time.Now()).Revision, "migration SQL is idempotent")
	events, err := r.Events(context.Background(), 1)
	require.NoError(t, err)
	require.Len(t, events, 1)
	require.Equal(t, "retire", events[0].Action)
	var schedulable bool
	require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=1`).Scan(&schedulable))
	require.False(t, schedulable)
}
func TestMetricStatsPostgresFinalOutcomesAndDeduplication(t *testing.T) {
	db := metricRulesDB(t)
	now := time.Now()
	at := now.Add(-time.Minute)
	_, err := db.Exec(`INSERT INTO usage_logs(created_at,request_id) VALUES($1,'good'),($1,'good'),($1,'partial'),($1,'other-key'),($1,'ws-turn')`, at)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO usage_logs(created_at,request_id,input_tokens,output_tokens,cache_read_tokens)VALUES($1,'placeholder',0,0,0);`, at)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE usage_logs SET request_type=3 WHERE request_id='ws-turn'`)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO ops_error_logs(created_at,request_id,api_key_id,request_type,is_count_tokens)VALUES
 ($1,'partial',1,2,false),($1,'partial',1,2,false),($1,'failure-only',1,2,false),($1,'other-key',2,2,false),($1,'ws-turn',1,3,false),($1,'count',1,2,true);`, at)
	require.NoError(t, err)
	stats, err := readOpsAccountRequestStats(context.Background(), db, []int64{1, 2}, now.Add(-time.Hour), now)
	require.NoError(t, err)
	require.EqualValues(t, 3, stats["1"].SuccessRequests, "dedupe, exclude placeholder and failed partial, preserve other API key and WS turns")
	require.EqualValues(t, 4, stats["1"].ErrorRequests, "dedupe terminal failures and exclude count_tokens")
	require.InDelta(t, 3.0/7, *stats["1"].SuccessRate, 1e-9)
	require.EqualValues(t, 3, stats["1"].CacheSamples)
	require.Nil(t, stats["2"])
}

func TestMetricRulesPostgresAuditFailureRollsBackPause(t *testing.T) {
	db := metricRulesDB(t)
	ctx := context.Background()
	r := &accountPerformanceRepository{db: db}
	now := time.Now()
	require.NoError(t, r.Save(ctx, 1, metricConfig(), 0, 42))
	_, err := db.Exec(`INSERT INTO ops_error_logs(created_at) SELECT $1 FROM generate_series(1,3)`, now.Add(-time.Second))
	require.NoError(t, err)
	_, err = db.Exec(`ALTER TABLE account_performance_events ADD CONSTRAINT reject_pause CHECK(action<>'pause')`)
	require.NoError(t, err)
	require.Error(t, r.Evaluate(ctx, 1, now))
	require.False(t, readMetric(t, r, now).Paused, "state and audit must commit together")
	_, err = db.Exec(`ALTER TABLE account_performance_events DROP CONSTRAINT reject_pause`)
	require.NoError(t, err)
	require.NoError(t, r.Evaluate(ctx, 1, now))
	m := readMetric(t, r, now)
	require.True(t, m.Paused)
	_, err = db.Exec(`UPDATE account_performance_monitors SET config=jsonb_set(config,'{auto_recover}','false'),paused_at=$1`, now.Add(-time.Hour))
	require.NoError(t, err)
	require.NoError(t, r.Evaluate(ctx, 1, now))
	require.Nil(t, readMetric(t, r, now).TrialStartedAt)
	require.NoError(t, r.Release(ctx, 1, m.Revision, m.PauseGeneration, 42))
	m = readMetric(t, r, now)
	require.False(t, m.Paused)
	require.False(t, m.Config.Enabled)
}

func TestMetricRulesPostgresRetireGroupSettingsWithoutChangingAccountRules(t *testing.T) {
	db := metricRulesDB(t)
	ctx := context.Background()
	repo := &accountPerformanceRepository{db: db}
	require.NoError(t, repo.Save(ctx, 1, metricConfig(), 0, 42))
	_, err := db.Exec(`INSERT INTO settings(key,value) VALUES
 ('group_anti_distillation:1','{"enabled":true,"pause_minutes":5}'),
 ('group_anti_distillation:2','{"enabled":false,"pause_minutes":10}'),
 ('group_rate_multiplier:1','2'),('ops_monitoring_enabled','true')`)
	require.NoError(t, err)
	raw, err := os.ReadFile("../../migrations/342_retire_group_zero_cache.sql")
	require.NoError(t, err)
	for range 2 {
		_, err = db.Exec(string(raw))
		require.NoError(t, err)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT COUNT(*) FROM settings`).Scan(&count))
	require.Equal(t, 2, count)
	var value string
	require.NoError(t, db.QueryRow(`SELECT value FROM settings WHERE key='group_rate_multiplier:1'`).Scan(&value))
	require.Equal(t, "2", value)
	m := readMetric(t, repo, time.Now())
	require.True(t, m.Config.Enabled)
	require.EqualValues(t, 1, m.Revision)
	var schedulable bool
	require.NoError(t, db.QueryRow(`SELECT schedulable FROM accounts WHERE id=1`).Scan(&schedulable))
	require.False(t, schedulable)
}
