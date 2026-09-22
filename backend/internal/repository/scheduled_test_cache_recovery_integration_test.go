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
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestScheduledTestCacheRecoveryIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("cache_recovery_%d", time.Now().UnixNano())
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
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	defer db.Close()
	db.SetMaxOpenConns(12)
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(`CREATE TABLE users(id BIGINT PRIMARY KEY);
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,extra JSONB DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ,is_exclusive BOOLEAN DEFAULT FALSE,subscription_type TEXT DEFAULT 'standard');
CREATE TABLE account_groups(account_id BIGINT,group_id BIGINT);
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_recovery_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO accounts(id,name) VALUES(62,'Recovery account');
INSERT INTO groups(id,name) VALUES(8,'Source group');
INSERT INTO account_groups VALUES(62,8);
CREATE TABLE usage_logs (
 id BIGSERIAL PRIMARY KEY, api_key_id BIGINT DEFAULT 1, request_id TEXT,
 group_id BIGINT DEFAULT 8, account_id BIGINT DEFAULT 62, created_at TIMESTAMPTZ NOT NULL,
 model TEXT DEFAULT 'model', requested_model TEXT, request_type SMALLINT DEFAULT 2,
 stream BOOLEAN DEFAULT true, openai_ws_mode BOOLEAN DEFAULT false, duration_ms INTEGER DEFAULT 0,
 actual_cost NUMERIC DEFAULT 0, total_cost NUMERIC DEFAULT 0,
 input_tokens BIGINT DEFAULT 0, output_tokens BIGINT DEFAULT 0,
 cache_creation_tokens BIGINT DEFAULT 0, cache_read_tokens BIGINT DEFAULT 0,
 image_input_tokens BIGINT DEFAULT 0, image_output_tokens BIGINT DEFAULT 0,
 image_count BIGINT DEFAULT 0, video_count BIGINT DEFAULT 0,
 first_token_ms INTEGER, native_compaction_v2 BOOLEAN DEFAULT false,
 inbound_endpoint TEXT, upstream_endpoint TEXT
);
CREATE TABLE ops_error_logs (
 id BIGSERIAL PRIMARY KEY, api_key_id BIGINT DEFAULT 1, request_id TEXT, client_request_id TEXT,
 group_id BIGINT DEFAULT 8, account_id BIGINT DEFAULT 62, created_at TIMESTAMPTZ NOT NULL,
 model TEXT DEFAULT 'model', requested_model TEXT, request_type SMALLINT DEFAULT 2, duration_ms INTEGER DEFAULT 0,
 status_code INTEGER DEFAULT 502, error_type TEXT DEFAULT 'upstream_error', is_count_tokens BOOLEAN DEFAULT false
);`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql", "249_scheduled_test_result_reasoning_effort.sql",
		"250_allow_group_account_scheduled_test_targets.sql", "251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql", "256_scheduled_test_multiple_definitions.sql", "257_scheduled_test_hourly_statistics.sql",
		"258_scheduled_test_protection.sql", "259_scheduled_test_outcome_actions.sql",
		"260_scheduled_test_model_check.sql", "261_scheduled_test_admin_review.sql", "262_scheduled_test_execution_snapshot.sql",
		"264_scheduled_test_cache_recovery.sql", "264_scheduled_test_cache_recovery.sql", "265_scheduled_test_generic_policy.sql", "268_scheduled_test_combination_states.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		exec(string(raw))
	}
	var definitionID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='hourly_stats'`).Scan(&definitionID))
	plans := &scheduledTestPlanRepository{db: db}
	repo := &scheduledTestResultRepository{db: db}
	groupID, accountID := int64(8), int64(62)
	rule := service.ScheduledTestProtectionRule{
		TestDefinitionID: definitionID, MinSamples: 20,
		Thresholds: []service.ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 80}},
		OnFail:     &service.ScheduledTestOutcomeAction{Scheduling: "pause"}, OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "resume"},
		Recovery: &service.ScheduledTestCacheRecoveryConfig{Enabled: true, CooldownSeconds: 60, TrialSeconds: 60, MaxRequests: 3, MinSamples: 2, RecoverRate: 90},
	}
	now := time.Now().UTC().Add(-10 * time.Second).Truncate(time.Microsecond)
	reset := func() {
		exec(`TRUNCATE scheduled_test_plans,scheduled_test_results,scheduled_test_plan_definitions,scheduled_test_protection_states,scheduled_test_managed_accounts,scheduled_test_votes,scheduler_outbox,usage_logs,ops_error_logs RESTART IDENTITY CASCADE`)
		exec(`UPDATE accounts SET status='active',schedulable=TRUE,deleted_at=NULL,extra='{}'`)
	}
	newHold := func(rule service.ScheduledTestProtectionRule, combinations ...service.ScheduledTestCombinationRule) (*service.ScheduledTestPlan, *service.ScheduledTestResult) {
		t.Helper()
		protection := service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{rule}}
		if len(combinations) > 0 {
			protection.Mode, protection.Combinations = "combined", combinations
		}
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{Name: "Cache recovery", GroupIDs: []int64{groupID}, GroupID: &groupID, TargetMode: "all_accounts",
			TestDefinitionID: &definitionID, TestDefinitionIDs: []int64{definitionID}, ModelID: "model", CronExpression: "0 * * * *", Enabled: true, MaxResults: 3,
			Protection: protection})
		require.NoError(t, err)
		result, err := repo.Create(ctx, &service.ScheduledTestResult{PlanID: p.ID, TestDefinitionID: &definitionID, GroupID: &groupID, AccountID: &accountID,
			TargetMode: "all_accounts", ModelID: "model", Status: "success", OutputKind: "statistics", StartedAt: now.Add(-time.Minute), FinishedAt: now,
			OutputStatistics: &service.ScheduledTestStatistics{WindowStart: now.Add(-time.Hour), WindowEnd: now}})
		require.NoError(t, err)
		exec(`UPDATE scheduled_test_plans SET latest_run_id=$2 WHERE id=$1`, p.ID, fmt.Sprintf("fixture-%d", p.ID))
		exec(`UPDATE scheduled_test_results SET run_id=$2 WHERE id=$1`, result.ID, fmt.Sprintf("fixture-%d", p.ID))
		result.RunID = fmt.Sprintf("fixture-%d", p.ID)
		result.OutputStatistics = &service.ScheduledTestStatistics{WindowStart: now.Add(-time.Hour), WindowEnd: now}
		require.NoError(t, repo.BeginProtection(ctx, result, rule))
		require.NoError(t, repo.CompleteProtection(ctx, result, "fail", "缓存率低于暂停阈值"))
		return p, result
	}
	state := func(planID int64) (string, bool, time.Time) {
		t.Helper()
		var phase string
		var blocked bool
		var until sql.NullTime
		require.NoError(t, db.QueryRowContext(ctx, `SELECT recovery_phase,blocked,recovery_cooldown_until FROM scheduled_test_protection_states WHERE plan_id=$1`, planID).Scan(&phase, &blocked, &until))
		return phase, blocked, until.Time
	}
	status := func(want string, trial bool) {
		t.Helper()
		var got string
		var marked bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status,COALESCE(extra->>'quality_protection_trial'='true',FALSE) FROM accounts WHERE id=62`).Scan(&got, &marked))
		require.Equal(t, want, got)
		require.Equal(t, trial, marked)
	}
	openTrial := func(planID int64) {
		t.Helper()
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1`, planID, now.Add(-time.Second))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
		phase, blocked, _ := state(planID)
		require.Equal(t, "trial", phase)
		require.True(t, blocked)
	}
	goodSamples := func() {
		t.Helper()
		exec(`INSERT INTO usage_logs(created_at,input_tokens,cache_read_tokens) VALUES($1,10,90),($1,5,95)`, now.Add(time.Second))
	}

	t.Run("cooldown ignores hourly completions and fresh trial restores scheduling", func(t *testing.T) {
		reset()
		p, result := newHold(rule)
		phase, blocked, cooldown := state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
		status("quality_paused", false)
		require.NoError(t, repo.CompleteProtection(ctx, result, "pass", "old hourly snapshot"))
		_, blocked, unchanged := state(p.ID)
		require.True(t, blocked)
		require.Equal(t, cooldown, unchanged)
		openTrial(p.ID)
		status("active", true)
		// Old poor samples and requests that started before trial are excluded.
		exec(`INSERT INTO usage_logs(created_at,input_tokens,duration_ms) VALUES($1,10000,0),($2,10000,30000)`, now.Add(-time.Minute), now.Add(time.Second))
		exec(`INSERT INTO usage_logs(created_at,input_tokens,request_type,stream) VALUES($1,10000,1,FALSE)`, now.Add(time.Second))
		goodSamples()
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(5*time.Second)))
		phase, blocked, _ = state(p.ID)
		require.Empty(t, phase)
		require.False(t, blocked)
		status("active", false)
		start, err := repo.CacheRecoveryWindowStart(ctx, p.ID, accountID, definitionID)
		require.NoError(t, err)
		require.Equal(t, now, *start)
		// A delayed old completion cannot pause an account after its trial passed.
		require.NoError(t, repo.CompleteProtection(ctx, result, "fail", "old hourly snapshot"))
		_, blocked, _ = state(p.ID)
		require.False(t, blocked)
		status("active", false)
		// A later snapshot beginning at the trial boundary may pause it again.
		result.OutputStatistics.WindowStart = *start
		require.NoError(t, repo.CompleteProtection(ctx, result, "fail", "fresh poor cache"))
		phase, blocked, _ = state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
	})

	t.Run("no traffic and insufficient eligible samples never pass", func(t *testing.T) {
		reset()
		p, _ := newHold(rule)
		openTrial(p.ID)
		// A synchronous success must not count toward the recovery sample quota.
		exec(`INSERT INTO usage_logs(created_at,input_tokens,cache_read_tokens,request_type,stream) VALUES($1,1,99,1,FALSE),($1,1,99,2,TRUE)`, now.Add(time.Second))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(5*time.Second)))
		phase, blocked, _ := state(p.ID)
		require.Equal(t, "trial", phase)
		require.True(t, blocked)
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(61*time.Second)))
		phase, blocked, until := state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
		require.Equal(t, now.Add(121*time.Second), until)
		status("quality_paused", false)
		exec(`TRUNCATE usage_logs`)
		openTrial(p.ID)
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(61*time.Second)))
		phase, blocked, _ = state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
	})

	t.Run("failed trial repeats cooldown without clearing protection", func(t *testing.T) {
		reset()
		p, _ := newHold(rule)
		openTrial(p.ID)
		exec(`INSERT INTO usage_logs(created_at,input_tokens,cache_read_tokens) VALUES($1,20,80),($1,20,80)`, now.Add(time.Second))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(5*time.Second)))
		phase, blocked, until := state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
		require.Equal(t, now.Add(65*time.Second), until)
		status("quality_paused", false)
	})

	t.Run("ordinary hold keeps recovery waiting without spending its trial", func(t *testing.T) {
		reset()
		p, _ := newHold(rule)
		otherRule := rule
		otherRule.Recovery = nil
		other, _ := newHold(otherRule)
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1`, p.ID, now.Add(-time.Second))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
		status("quality_paused", false)
		phase, blocked, _ := state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
		_, blocked, _ = state(other.ID)
		require.True(t, blocked)
		status("quality_paused", false)
	})

	t.Run("staggered cooldowns open overlapping trials when every hold is ready", func(t *testing.T) {
		reset()
		p, _ := newHold(rule)
		otherRule := rule
		otherRule.Recovery = &service.ScheduledTestCacheRecoveryConfig{Enabled: true, CooldownSeconds: 300, TrialSeconds: 60, MaxRequests: 3, MinSamples: 2, RecoverRate: 90}
		other, _ := newHold(otherRule)
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1`, p.ID, now.Add(-time.Minute))
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1`, other.ID, now.Add(3*time.Minute))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
		phase, _, _ := state(p.ID)
		require.Equal(t, "cooldown", phase)
		status("quality_paused", false)
		// Once the later hold becomes due both use the same sweep boundary.
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1`, other.ID, now.Add(-time.Second))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
		status("active", true)
		for _, id := range []int64{p.ID, other.ID} {
			start, err := repo.CacheRecoveryWindowStart(ctx, id, accountID, definitionID)
			require.NoError(t, err)
			require.Equal(t, now, *start)
		}
	})

	t.Run("all active trials can admit traffic and concurrent sweeps are idempotent", func(t *testing.T) {
		reset()
		p, _ := newHold(rule)
		other, _ := newHold(rule)
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$1`, now.Add(-time.Second))
		var wg sync.WaitGroup
		errs := make(chan error, 8)
		for i := 0; i < 8; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				errs <- repo.AdvanceCacheRecovery(ctx, now)
			}()
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		status("active", true)
		for _, id := range []int64{p.ID, other.ID} {
			start, err := repo.CacheRecoveryWindowStart(ctx, id, accountID, definitionID)
			require.NoError(t, err)
			require.Equal(t, now, *start)
		}
	})
	t.Run("combined mode retains cache safeguard and combination pause blocks its trial", func(t *testing.T) {
		reset()
		p, _ := newHold(rule, service.ScheduledTestCombinationRule{ID: "observe", Condition: combinedTestLeaf(definitionID, "fail"), Action: service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}})
		phase, blocked, _ := state(p.ID)
		require.Equal(t, "cooldown", phase)
		require.True(t, blocked)
		exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,blocked,reason) VALUES($1,62,TRUE,'combination pause')`, p.ID)
		exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$2 WHERE plan_id=$1`, p.ID, now.Add(-time.Second))
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
		phase, _, _ = state(p.ID)
		require.Equal(t, "cooldown", phase)
		status("quality_paused", false)
		exec(`UPDATE scheduled_test_combination_states SET blocked=FALSE WHERE plan_id=$1`, p.ID)
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
		phase, blocked, _ = state(p.ID)
		require.Equal(t, "trial", phase)
		require.True(t, blocked)
		status("active", true)
	})

	t.Run("manual status and schedulability remain authoritative", func(t *testing.T) {
		for _, manual := range []string{"status='error'", "status='inactive'", "schedulable=FALSE", "deleted_at=NOW()"} {
			reset()
			p, _ := newHold(rule)
			exec(`UPDATE accounts SET ` + manual + ` WHERE id=62`)
			exec(`UPDATE scheduled_test_protection_states SET recovery_cooldown_until=$1`, now.Add(-time.Second))
			require.NoError(t, repo.AdvanceCacheRecovery(ctx, now))
			phase, blocked, _ := state(p.ID)
			require.Equal(t, "cooldown", phase)
			require.True(t, blocked)
			condition := manual
			if manual == "deleted_at=NOW()" {
				condition = "deleted_at IS NOT NULL"
			}
			var unchanged bool
			require.NoError(t, db.QueryRowContext(ctx, `SELECT `+condition+` FROM accounts WHERE id=62`).Scan(&unchanged))
			require.True(t, unchanged)
		}
	})

	t.Run("clearing plan protection removes trial and preserves another hold", func(t *testing.T) {
		reset()
		p, _ := newHold(rule)
		openTrial(p.ID)
		status("active", true)
		require.NoError(t, repo.ClearPlanProtection(ctx, p.ID))
		status("active", false)
		start, err := repo.CacheRecoveryWindowStart(ctx, p.ID, accountID, definitionID)
		require.NoError(t, err)
		require.Nil(t, start)
		p, _ = newHold(rule)
		otherRule := rule
		otherRule.Recovery = nil
		other, _ := newHold(otherRule)
		require.NoError(t, repo.ClearPlanProtection(ctx, p.ID))
		status("quality_paused", false)
		_, blocked, _ := state(other.ID)
		require.True(t, blocked)
	})

	t.Run("recovery state survives result retention cleanup", func(t *testing.T) {
		reset()
		p, result := newHold(rule)
		exec(`DELETE FROM scheduled_test_results WHERE id=$1`, result.ID)
		openTrial(p.ID)
		goodSamples()
		require.NoError(t, repo.AdvanceCacheRecovery(ctx, now.Add(5*time.Second)))
		phase, blocked, _ := state(p.ID)
		require.Empty(t, phase)
		require.False(t, blocked)
		status("active", false)
	})
}
