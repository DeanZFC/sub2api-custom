//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type passRateFixture struct {
	t            *testing.T
	ctx          context.Context
	db           *sql.DB
	repo         *scheduledTestResultRepository
	plans        *scheduledTestPlanRepository
	plan         *service.ScheduledTestPlan
	definitionID int64
	sequence     int
}

func newPassRateFixture(t *testing.T, window int) *passRateFixture {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("active_pass_rate_%d", time.Now().UnixNano())
	_, err = admin.ExecContext(ctx, "CREATE SCHEMA "+pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, cleanupErr := admin.ExecContext(context.Background(), "DROP SCHEMA "+pq.QuoteIdentifier(schema)+" CASCADE")
		require.NoError(t, cleanupErr)
		require.NoError(t, admin.Close())
	})
	parsed, err := url.Parse(dsn)
	require.NoError(t, err)
	q := parsed.Query()
	q.Set("search_path", schema)
	parsed.RawQuery = q.Encode()
	db, err := sql.Open("postgres", parsed.String())
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, db.Close()) })
	f := &passRateFixture{t: t, ctx: ctx, db: db, repo: &scheduledTestResultRepository{db: db}, plans: &scheduledTestPlanRepository{db: db}}
	f.exec(`CREATE TABLE users(id BIGINT PRIMARY KEY);
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,extra JSONB DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,platform TEXT NOT NULL DEFAULT 'openai',status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ,is_exclusive BOOLEAN DEFAULT FALSE,subscription_type TEXT DEFAULT 'standard');
CREATE TABLE account_groups(account_id BIGINT,group_id BIGINT);
CREATE TABLE user_allowed_groups(user_id BIGINT,group_id BIGINT);
CREATE TABLE user_subscriptions(user_id BIGINT,group_id BIGINT,deleted_at TIMESTAMPTZ,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ);
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_pass_rate_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO users VALUES(1);
INSERT INTO accounts(id,name) VALUES(62,'Test account'),(63,'Second account');
INSERT INTO groups(id,name) VALUES(8,'Test group');
INSERT INTO account_groups VALUES(62,8),(63,8);`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql", "249_scheduled_test_result_reasoning_effort.sql",
		"250_allow_group_account_scheduled_test_targets.sql", "251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql", "256_scheduled_test_multiple_definitions.sql", "257_scheduled_test_hourly_statistics.sql",
		"258_scheduled_test_protection.sql", "259_scheduled_test_outcome_actions.sql", "260_scheduled_test_model_check.sql", "261_scheduled_test_admin_review.sql",
		"262_scheduled_test_execution_snapshot.sql", "281_scheduled_test_membership_revocation.sql", "264_scheduled_test_cache_recovery.sql",
		"265_scheduled_test_generic_policy.sql", "268_scheduled_test_combination_states.sql", "270_scheduled_test_time_periods.sql",
		"282_scheduled_test_excluded_accounts.sql", "276_scheduled_test_cost_control.sql", "284_scheduled_test_output_tokens.sql",
		"309_quality_recovery_budget_evidence.sql", "312_scheduled_test_pass_rate.sql", "315_scheduled_test_multi_model.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		f.exec(string(raw))
	}
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='candy'`).Scan(&f.definitionID))
	group := int64(8)
	rule := service.ScheduledTestProtectionRule{TestDefinitionID: f.definitionID, ExpectedAnswer: "21", AnswerMatch: "numeric",
		OnPass: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}, OnFail: &service.ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"},
		PassRate: &service.ScheduledTestPassRateConfig{Enabled: true, WindowSize: window, WindowSeconds: 604800, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 2}}
	f.plan, err = f.plans.Create(ctx, &service.ScheduledTestPlan{Name: "Pass rate", GroupID: &group, GroupIDs: []int64{group}, TargetMode: "all_accounts", TestDefinitionID: &f.definitionID, TestDefinitionIDs: []int64{f.definitionID}, ModelID: "model-a", Enabled: true, MaxResults: 2, CronExpression: "0 * * * *", Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: []service.ScheduledTestProtectionRule{rule}}})
	require.NoError(t, err)
	return f
}

func (f *passRateFixture) exec(query string, args ...any) {
	f.t.Helper()
	_, err := f.db.ExecContext(f.ctx, query, args...)
	require.NoError(f.t, err)
}

func (f *passRateFixture) begin() *service.ScheduledTestResult {
	f.t.Helper()
	f.sequence++
	at := time.Now().UTC().Add(-12*time.Hour + time.Duration(f.sequence)*time.Minute).Truncate(time.Microsecond)
	account, definition := int64(62), f.definitionID
	rows, err := f.repo.BeginRun(f.ctx, f.plan, fmt.Sprintf("pass-rate-run-%d", f.sequence), []*service.ScheduledTestResult{{PlanID: f.plan.ID, AccountID: &account, GroupID: f.plan.GroupID, TestDefinitionID: &definition, TargetMode: f.plan.TargetMode, ModelID: f.plan.ModelID, ReasoningEffort: f.plan.ReasoningEffort, Status: "running", OutputKind: "number", StartedAt: at, FinishedAt: at}})
	require.NoError(f.t, err)
	require.Len(f.t, rows, 1)
	require.NoError(f.t, f.repo.BeginProtectionRun(f.ctx, f.plan, at))
	require.NoError(f.t, f.repo.BeginProtection(f.ctx, rows[0], f.plan.Protection.Rules[0]))
	return rows[0]
}

func (f *passRateFixture) complete(row *service.ScheduledTestResult, status, verdict string) {
	f.t.Helper()
	row.Status = status
	row.ResponseText = "21"
	if verdict == "fail" {
		row.ResponseText = "20"
	}
	row.FinishedAt = time.Now().UTC()
	require.NoError(f.t, f.repo.Update(f.ctx, row))
	require.NoError(f.t, f.repo.CompleteProtection(f.ctx, row, verdict, "fixture "+verdict))
}

func (f *passRateFixture) result(verdict string) *service.ScheduledTestResult {
	row := f.begin()
	f.complete(row, "success", verdict)
	return row
}

func (f *passRateFixture) summary(model string) service.ScheduledTestPassRateSummary {
	f.t.Helper()
	var raw []byte
	require.NoError(f.t, f.db.QueryRowContext(f.ctx, `SELECT summary FROM scheduled_test_pass_rate_states WHERE plan_id=$1 AND account_id=62 AND test_definition_id=$2 AND model_key=$3`, f.plan.ID, f.definitionID, model).Scan(&raw))
	var s service.ScheduledTestPassRateSummary
	require.NoError(f.t, json.Unmarshal(raw, &s))
	return s
}

func (f *passRateFixture) evidenceCount() int {
	f.t.Helper()
	var count int
	require.NoError(f.t, f.db.QueryRowContext(f.ctx, `SELECT COUNT(*) FROM scheduled_test_pass_rate_evidence WHERE plan_id=$1`, f.plan.ID).Scan(&count))
	return count
}

func TestScheduledTestPassRateIntegration(t *testing.T) {
	t.Run("strict boundary and full recovery window", func(t *testing.T) {
		f := newPassRateFixture(t, 10)
		for _, v := range []string{"fail", "fail", "pass", "pass", "pass", "pass", "pass", "pass", "pass", "pass"} {
			f.result(v)
		}
		s := f.summary("model-a")
		require.True(t, s.Ready)
		require.Equal(t, 8, s.Passed)
		require.Equal(t, float64(80), *s.PassRate)
		require.False(t, s.Blocked)
		for i := 0; i < 4; i++ {
			f.result("fail")
		}
		s = f.summary("model-a")
		require.True(t, s.Blocked)
		for i := 0; i < 8; i++ {
			f.result("pass")
		}
		s = f.summary("model-a")
		require.Equal(t, 8, s.Passed)
		require.True(t, s.Blocked)
		f.result("pass")
		s = f.summary("model-a")
		require.Equal(t, 9, s.Passed)
		require.GreaterOrEqual(t, s.ConsecutivePasses, 2)
		require.False(t, s.Blocked)
		var status string
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
		require.Equal(t, "active", status, "model hold must not globally pause the account")
	})
	t.Run("execution failures and retries do not fabricate or overwrite evidence", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		row := f.begin()
		f.complete(row, "failed", "fail")
		require.Zero(t, f.evidenceCount())
		row.StartedAt = row.StartedAt.Add(time.Second)
		row.FinishedAt = row.StartedAt
		require.NoError(t, f.repo.RestartFailed(f.ctx, row))
		require.NoError(t, f.repo.BeginProtection(f.ctx, row, f.plan.Protection.Rules[0]))
		f.complete(row, "success", "fail")
		require.Equal(t, 1, f.evidenceCount())
		require.NoError(t, f.repo.CompleteProtection(f.ctx, row, "pass", "duplicate successful retry"))
		require.Equal(t, 1, f.evidenceCount())
		var v string
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT verdict FROM scheduled_test_pass_rate_evidence WHERE result_id=$1`, row.ID).Scan(&v))
		require.Equal(t, "fail", v)
		f.result("pass")
		require.True(t, f.summary("model-a").Blocked)
		f.exec(`DELETE FROM scheduled_test_results WHERE id=$1`, row.ID)
		require.Equal(t, 2, f.evidenceCount(), "history pruning must retain effective evidence")
	})
	t.Run("model and rule versions do not share evidence or clear old holds", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("fail")
		f.result("fail")
		old := f.summary("model-a")
		require.True(t, old.Blocked)
		f.plan.ModelID = "model-b"
		f.plan.ModelIDs = []string{"model-b"}
		var err error
		f.plan, err = f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		f.result("pass")
		fresh := f.summary("model-b")
		require.Equal(t, 1, fresh.Samples)
		require.False(t, fresh.Ready)
		require.False(t, fresh.Blocked)
		require.True(t, f.summary("model-a").Blocked)
		f.plan.ModelID = "model-a"
		f.plan.ModelIDs = []string{"model-a"}
		f.plan, err = f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		f.exec(`UPDATE scheduled_test_definitions SET prompt=prompt||' revised',updated_at=NOW() WHERE id=$1`, f.definitionID)
		f.result("pass")
		fresh = f.summary("model-a")
		require.NotEqual(t, old.RuleVersion, fresh.RuleVersion)
		require.Equal(t, 1, fresh.Samples)
		require.True(t, fresh.Blocked, "rule edit cannot release an existing hold")
		f.result("pass")
		require.False(t, f.summary("model-a").Blocked)
	})
	t.Run("live admission rejects held model and missing model only", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.plan.ModelIDs = []string{"model-a", "model-b"}
		var err error
		f.plan, err = f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		f.result("fail")
		f.result("fail")
		repo := &accountRepository{sql: f.db}
		for _, test := range []struct {
			model string
			want  bool
		}{{"model-a", false}, {"model-b", true}, {"", false}} {
			ctx := context.WithValue(f.ctx, ctxkey.Model, test.model)
			allowed, err := repo.AcquireCacheRecoveryRequest(ctx, 62)
			require.NoError(t, err)
			require.Equal(t, test.want, allowed, test.model)
		}
	})
	t.Run("duplicate completion and review do not release an existing hold", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("pass")
		latest := f.result("pass")
		require.False(t, f.summary("model-a").Blocked)
		f.exec(`UPDATE scheduled_test_pass_rate_states SET blocked=TRUE,summary=jsonb_set(summary,'{blocked}','true'::jsonb) WHERE plan_id=$1`, f.plan.ID)
		for i := 0; i < 2; i++ {
			require.NoError(t, f.repo.CompleteProtection(f.ctx, latest, "pass", "repeated completion or review"))
			require.True(t, f.summary("model-a").Blocked, "old passing evidence must not release an existing hold")
			require.Equal(t, 2, f.evidenceCount())
		}
		f.result("pass")
		require.False(t, f.summary("model-a").Blocked, "a fresh passing round may complete recovery")
	})
	t.Run("history shows a fresh window without changing enforcement", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("fail")
		latest := f.result("fail")
		require.True(t, f.summary("model-a").Blocked)
		f.exec(`UPDATE scheduled_test_pass_rate_evidence SET started_at=NOW()-INTERVAL '8 days' WHERE plan_id=$1`, f.plan.ID)
		result, err := f.repo.GetByID(f.ctx, latest.ID)
		require.NoError(t, err)
		require.NotNil(t, result.PassRate)
		require.True(t, result.PassRate.Blocked)
		require.True(t, result.PassRate.Stale)
		require.Zero(t, result.PassRate.Samples)
		require.Nil(t, result.PassRate.PassRate)
		require.True(t, f.summary("model-a").Blocked, "reading stale evidence cannot clear enforcement")
	})
	t.Run("disabling pass rate or deleting the plan clears its routing projection", func(t *testing.T) {
		for _, action := range []string{"disable", "delete"} {
			t.Run(action, func(t *testing.T) {
				f := newPassRateFixture(t, 2)
				f.result("fail")
				f.result("fail")
				require.True(t, f.summary("model-a").Blocked)
				var projection bool
				require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT extra ? 'active_pass_rate_holds' FROM accounts WHERE id=62`).Scan(&projection))
				require.True(t, projection)
				if action == "disable" {
					f.plan.Protection.Rules[0].PassRate.Enabled = false
					var err error
					f.plan, err = f.plans.Update(f.ctx, f.plan)
					require.NoError(t, err)
				} else {
					require.NoError(t, f.plans.Delete(f.ctx, f.plan.ID))
				}
				var holds int
				require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM scheduled_test_pass_rate_states WHERE plan_id=$1 AND blocked`, f.plan.ID).Scan(&holds))
				require.Zero(t, holds)
				require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT extra ? 'active_pass_rate_holds' FROM accounts WHERE id=62`).Scan(&projection))
				require.False(t, projection)
				allowed, err := (&accountRepository{sql: f.db}).AcquireCacheRecoveryRequest(context.WithValue(f.ctx, ctxkey.Model, "model-a"), 62)
				require.NoError(t, err)
				require.True(t, allowed)
			})
		}
	})
	t.Run("changing thresholds or schedule does not clear an existing hold", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("fail")
		f.result("fail")
		f.plan.CronExpression = "15 * * * *"
		f.plan.Protection.Rules[0].PassRate.PauseBelow = 0
		f.plan.Protection.Rules[0].PassRate.RecoverAtLeast = 0
		var err error
		f.plan, err = f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		require.True(t, f.summary("model-a").Blocked)
		allowed, err := (&accountRepository{sql: f.db}).AcquireCacheRecoveryRequest(context.WithValue(f.ctx, ctxkey.Model, "model-a"), 62)
		require.NoError(t, err)
		require.False(t, allowed)
	})
	t.Run("an in-flight result cannot be assigned to a revised test definition", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		inflight := f.begin()
		f.exec(`UPDATE scheduled_test_definitions SET prompt=prompt||' revised during execution',updated_at=NOW() WHERE id=$1`, f.definitionID)
		f.complete(inflight, "success", "pass")
		require.Zero(t, f.evidenceCount(), "a stale execution must not contaminate the new test definition")
		f.result("pass")
		require.Equal(t, 1, f.evidenceCount())
		require.Equal(t, 1, f.summary("model-a").Samples)
	})
	t.Run("removing one plan never releases a hold owned by another plan", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("fail")
		f.result("fail")
		first := f.plan
		second := *first
		second.Name = "Second independent pass-rate rule"
		var err error
		f.plan, err = f.plans.Create(f.ctx, &second)
		require.NoError(t, err)
		require.NotEqual(t, first.ID, f.plan.ID)
		f.result("fail")
		f.result("fail")
		require.NoError(t, f.plans.Delete(f.ctx, f.plan.ID))
		ctx := context.WithValue(f.ctx, ctxkey.Model, "model-a")
		repo := &accountRepository{sql: f.db}
		allowed, err := repo.AcquireCacheRecoveryRequest(ctx, 62)
		require.NoError(t, err)
		require.False(t, allowed, "the remaining plan still owns its model hold")
		require.NoError(t, f.plans.Delete(f.ctx, first.ID))
		allowed, err = repo.AcquireCacheRecoveryRequest(ctx, 62)
		require.NoError(t, err)
		require.True(t, allowed)
	})
	t.Run("definition edited after input resolution is rejected before protection begins", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		var prompt, kind string
		var maxOutputTokens int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT prompt,output_kind,max_output_tokens FROM scheduled_test_definitions WHERE id=$1`, f.definitionID).Scan(&prompt, &kind, &maxOutputTokens))
		resolved := service.WithScheduledTestPassRateSnapshot(f.ctx, prompt, kind, maxOutputTokens)
		f.sequence++
		at := time.Now().UTC().Add(-time.Hour).Truncate(time.Microsecond)
		account, definition := int64(62), f.definitionID
		rows, err := f.repo.BeginRun(f.ctx, f.plan, "resolved-input-race", []*service.ScheduledTestResult{{PlanID: f.plan.ID, AccountID: &account, GroupID: f.plan.GroupID, TestDefinitionID: &definition, TargetMode: f.plan.TargetMode, ModelID: f.plan.ModelID, Status: "running", OutputKind: kind, StartedAt: at, FinishedAt: at}})
		require.NoError(t, err)
		require.NoError(t, f.repo.BeginProtectionRun(f.ctx, f.plan, at))
		f.exec(`UPDATE scheduled_test_definitions SET prompt=prompt||' revised before protection',updated_at=NOW() WHERE id=$1`, f.definitionID)
		err = f.repo.BeginProtection(resolved, rows[0], f.plan.Protection.Rules[0])
		require.ErrorContains(t, err, "test definition changed before execution")
		require.Zero(t, f.evidenceCount())
	})
	t.Run("administrator review list includes current pass-rate evidence", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.plan.Protection.Rules[0].Vote = &service.ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1, RejectAbove: 0}
		f.plan.Protection.Rules[0].PassRate.PauseOnSingleFailure = true
		var err error
		f.plan, err = f.plans.Update(f.ctx, f.plan)
		require.NoError(t, err)
		row := f.result("pass")
		var generation int64
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT generation FROM scheduled_test_protection_states WHERE result_id=$1`, row.ID).Scan(&generation))
		require.NoError(t, f.repo.DecideTestResult(f.ctx, 1, row.ID, generation, "fail"))
		reviews, err := f.repo.ListAdminReviews(f.ctx)
		require.NoError(t, err)
		require.Len(t, reviews, 1)
		require.NotNil(t, reviews[0].Result.PassRate)
		require.Equal(t, 1, reviews[0].Result.PassRate.Samples)
		require.True(t, reviews[0].Result.PassRate.Blocked)
		require.Equal(t, "fail", reviews[0].Result.PassRate.LatestVerdict)
	})
	t.Run("persisted output contract failures count without counting upstream errors", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		row := f.begin()
		row.ExecutionInfo = &service.ScheduledTestExecutionInfo{OutputContractFailed: true}
		f.complete(row, "failed", "fail")
		require.Equal(t, 1, f.evidenceCount())
		s := f.summary("model-a")
		require.Equal(t, 1, s.Samples)
		require.Equal(t, "fail", s.LatestVerdict)
		row = f.begin()
		f.complete(row, "failed", "fail")
		require.Equal(t, 1, f.evidenceCount(), "an upstream execution error remains outside the denominator")
		row = f.begin()
		row.ExecutionInfo = &service.ScheduledTestExecutionInfo{OutputContractFailed: true}
		f.complete(row, "failed", "fail")
		require.True(t, f.summary("model-a").Blocked)
	})
}

func TestScheduledTestAllPassRecoversOnNextCompleteRoundIntegration(t *testing.T) {
	f := newPassRateFixture(t, 10)
	f.plan.Protection.Mode = "all_pass"
	// The service normalizes all-pass plans to a one-result gate.
	gate := f.plan.Protection.Rules[0].PassRate
	gate.WindowSize, gate.RecoverConsecutive = 1, 1
	gate.PauseBelow, gate.RecoverAtLeast, gate.PauseOnSingleFailure = 100, 100, true
	var err error
	f.plan, err = f.plans.Update(f.ctx, f.plan)
	require.NoError(t, err)
	f.result("fail")
	require.True(t, f.summary("model-a").Blocked)
	next := f.begin()
	var status string
	require.NoError(t, f.db.QueryRow(`SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "quality_paused", status, "starting the next round must not release the hold")
	f.complete(next, "success", "pass")
	require.False(t, f.summary("model-a").Blocked, "one newly completed successful round is sufficient")
	require.NoError(t, f.db.QueryRow(`SELECT status FROM accounts WHERE id=62`).Scan(&status))
	require.Equal(t, "active", status)
	f.result("fail")
	require.True(t, f.summary("model-a").Blocked)
}
