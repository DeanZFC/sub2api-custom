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

	"github.com/Wei-Shaw/sub2api/internal/service"
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
		"261_scheduled_test_admin_review.sql", "261_scheduled_test_admin_review.sql",
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
	groupID, accountID := int64(8), int64(62)
	ruleFor := func(definitionID int64, passGroups, failGroups []int64, voting bool) service.ScheduledTestProtectionRule {
		currentT.Helper()
		raw := map[string]any{
			"test_definition_id": definitionID, "pause_on_failure": true,
			"on_pass": map[string]any{"scheduling": "keep", "group_mode": "assign", "group_ids": passGroups},
			"on_fail": map[string]any{"scheduling": "keep", "group_mode": "assign", "group_ids": failGroups},
		}
		if voting {
			raw["vote"] = map[string]any{"enabled": true, "reject_above": 1, "pass_at_least": 2}
		}
		encoded, err := json.Marshal(raw)
		require.NoError(currentT, err)
		var rule service.ScheduledTestProtectionRule
		require.NoError(currentT, json.Unmarshal(encoded, &rule))
		return rule
	}
	reset := func(testT *testing.T) {
		currentT = testT
		exec(`TRUNCATE scheduled_test_plans,scheduled_test_results,scheduled_test_plan_definitions,scheduled_test_protection_states,scheduled_test_managed_accounts,scheduled_test_votes,scheduler_outbox RESTART IDENTITY CASCADE`)
		exec(`UPDATE accounts SET platform='openai',status='active',schedulable=TRUE,deleted_at=NULL,extra='{}';
UPDATE groups SET platform='openai',status='active',deleted_at=NULL;
TRUNCATE user_allowed_groups,user_subscriptions,account_groups;
INSERT INTO account_groups(account_id,group_id,priority) VALUES(62,8,71),(62,9,72),(63,8,73);`)
	}
	planInput := func(rules ...service.ScheduledTestProtectionRule) *service.ScheduledTestPlan {
		ids := make([]int64, 0, len(rules))
		for _, rule := range rules {
			ids = append(ids, rule.TestDefinitionID)
		}
		return &service.ScheduledTestPlan{Name: "Outcome actions", GroupID: &groupID, AccountID: &accountID, TargetMode: "account", TestDefinitionID: &ids[0], TestDefinitionIDs: ids, ModelID: "model", CronExpression: "* * * * *", Enabled: true, MaxResults: 3, Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: rules}}
	}
	newPlan := func(rules ...service.ScheduledTestProtectionRule) *service.ScheduledTestPlan {
		currentT.Helper()
		p, err := plans.Create(ctx, planInput(rules...))
		require.NoError(currentT, err)
		return p
	}
	sequence := int64(0)
	begin := func(plan *service.ScheduledTestPlan, rule service.ScheduledTestProtectionRule) *service.ScheduledTestResult {
		currentT.Helper()
		sequence++
		started := time.Now().UTC().Add(time.Duration(sequence) * time.Second).Truncate(time.Microsecond)
		result, err := repo.Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, TestDefinitionID: &rule.TestDefinitionID, GroupID: plan.GroupID, AccountID: &accountID, TargetMode: plan.TargetMode, ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, Status: "running", OutputKind: "html", StartedAt: started, FinishedAt: started})
		require.NoError(currentT, err)
		require.NoError(currentT, repo.BeginProtection(ctx, result, rule))
		return result
	}
	finish := func(result *service.ScheduledTestResult, verdict string) error {
		currentT.Helper()
		result.Status, result.ResponseText, result.OutputHTML = "success", "safe output", "<p>safe output</p>"
		require.NoError(currentT, repo.Update(ctx, result))
		return repo.CompleteProtection(ctx, result, verdict, "quality condition failed")
	}
	complete := func(result *service.ScheduledTestResult, verdict string) {
		currentT.Helper()
		require.NoError(currentT, finish(result, verdict))
	}
	bindings := func() []int64 {
		currentT.Helper()
		var ids pq.Int64Array
		require.NoError(currentT, db.QueryRowContext(ctx, `SELECT COALESCE(array_agg(group_id ORDER BY group_id),'{}'::bigint[]) FROM account_groups WHERE account_id=62`).Scan(&ids))
		return []int64(ids)
	}
	cast := func(userID, resultID int64, vote string) *service.ScheduledTestVoteResult {
		currentT.Helper()
		out, err := repo.CastTestVote(ctx, userID, resultID, vote)
		require.NoError(currentT, err)
		return out
	}
	assertActive := func(t *testing.T) {
		currentT.Helper()
		var status string
		require.NoError(currentT, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&status))
		require.Equal(currentT, "active", status, "group-only outcomes must not pause the account")
	}
	startRound := func(plan *service.ScheduledTestPlan) time.Time {
		currentT.Helper()
		sequence++
		started := time.Now().UTC().Add(time.Duration(sequence) * time.Second).Truncate(time.Microsecond)
		require.NoError(currentT, repo.BeginProtectionRun(ctx, plan, started))
		return started
	}

	t.Run("administrator verdict bypasses vote counts but preserves automatic failures", func(t *testing.T) {
		reset(t)
		candy := ruleFor(candyID, []int64{10}, []int64{8, 11}, false)
		manual := ruleFor(pelicanID, []int64{10}, []int64{8, 11}, true)
		p := newPlan(candy, manual)
		complete(begin(p, candy), "pass")
		result := begin(p, manual)
		complete(result, "pass")
		reviews, err := repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Len(t, reviews, 1)
		require.Equal(t, "Secret account name", reviews[0].Result.AccountName)
		generation := reviews[0].Generation
		require.NoError(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "pass"))
		require.Equal(t, []int64{9, 10}, bindings(), "one administrator decision upgrades without counted votes")
		require.NoError(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "fail"))
		require.Equal(t, []int64{8, 9, 11}, bindings(), "downgrade assigns both configured lower groups")
		complete(begin(p, candy), "pass")
		require.Equal(t, []int64{8, 9, 11}, bindings(), "another good sample must not erase the manual veto")
		complete(begin(p, candy), "fail")
		require.NoError(t, repo.DecideTestResult(ctx, 2, result.ID, generation, "pass"))
		require.Equal(t, []int64{8, 9, 11}, bindings(), "administrator pass cannot override another failed check")
		complete(begin(p, candy), "pass")
		require.Equal(t, []int64{9, 10}, bindings())
		reviews, err = repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Equal(t, "pass", reviews[0].AdminVerdict)
		require.Equal(t, int64(2), *reviews[0].AdminUserID)
		require.NotNil(t, reviews[0].DecidedAt)
		exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
		require.ErrorIs(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "fail"), service.ErrScheduledTestVoteUnavailable)
		reviews, err = repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Empty(t, reviews)
		require.Equal(t, []int64{9, 10}, bindings())
	})

	t.Run("administrator review handles paused accounts and rejects stale rounds", func(t *testing.T) {
		reset(t)
		rule := ruleFor(pelicanID, []int64{10}, []int64{8}, true)
		rule.OnPass.Scheduling, rule.OnFail.Scheduling = "resume", "pause"
		p := newPlan(rule)
		result := begin(p, rule)
		complete(result, "pass")
		reviews, err := repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		generation := reviews[0].Generation
		require.NoError(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "fail"))
		reviews, err = repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Len(t, reviews, 1)
		require.True(t, reviews[0].AccountPaused)
		require.NoError(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "pass"))
		assertActive(t)
		require.Equal(t, []int64{9, 10}, bindings())
		startRound(p)
		require.ErrorIs(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "fail"), service.ErrScheduledTestVoteUnavailable)
		result = begin(p, rule)
		complete(result, "pass")
		reviews, err = repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Len(t, reviews, 1)
		require.Empty(t, reviews[0].AdminVerdict)
		require.Nil(t, reviews[0].AdminUserID)
		require.Nil(t, reviews[0].DecidedAt)
		require.ErrorIs(t, repo.DecideTestResult(ctx, 1, result.ID, generation, "fail"), service.ErrScheduledTestVoteUnavailable)
		// A retry may have replaced the row before BeginProtection obtains its lock.
		exec(`UPDATE scheduled_test_results SET started_at=started_at+interval '1 second' WHERE id=$1`, result.ID)
		require.ErrorIs(t, repo.DecideTestResult(ctx, 1, result.ID, reviews[0].Generation, "fail"), service.ErrScheduledTestVoteUnavailable)
		reviews, err = repo.ListAdminReviews(ctx)
		require.NoError(t, err)
		require.Empty(t, reviews)
	})

	t.Run("shared scope requires every type to pass and a failed type wins", func(t *testing.T) {
		reset(t)
		candy := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
		pelican := ruleFor(pelicanID, []int64{10, 11}, []int64{8}, false)
		p := newPlan(candy, pelican)
		complete(begin(p, candy), "pass")
		require.Equal(t, []int64{8, 9}, bindings(), "a definition not run yet must not be treated as passing")
		complete(begin(p, pelican), "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		assertActive(t)
		var unrelatedPriority int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT priority FROM account_groups WHERE account_id=62 AND group_id=9`).Scan(&unrelatedPriority))
		require.Equal(t, 72, unrelatedPriority, "unmanaged bindings must retain their priority")
		complete(begin(p, candy), "fail")
		require.Equal(t, []int64{8, 9}, bindings())
		complete(begin(p, pelican), "pass")
		require.Equal(t, []int64{8, 9}, bindings(), "a later passing check cannot overwrite a failing one")
		assertActive(t)
		complete(begin(p, candy), "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		pending := begin(p, candy)
		require.Equal(t, []int64{9, 10, 11}, bindings(), "starting a new round retains the applied outcome")
		complete(pending, "pending")
		require.Equal(t, []int64{9, 10, 11}, bindings(), "insufficient samples retain the applied outcome")
	})

	t.Run("moved accounts remain detected and votes can upgrade and downgrade", func(t *testing.T) {
		reset(t)
		voting := ruleFor(pelicanID, []int64{10, 11}, []int64{8}, true)
		p := newPlan(voting)
		r := begin(p, voting)
		complete(r, "pass")
		cast(1, r.ID, "pass")
		require.Equal(t, []int64{8, 9}, bindings())
		cast(2, r.ID, "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		ids, err := repo.ListPlanDetectionAccountIDs(ctx, p, p.AccountID)
		require.NoError(t, err)
		require.Equal(t, []int64{62}, ids, "source-group removal must not lose managed accounts")
		visible, err := repo.ListVotingResults(ctx, 3)
		require.NoError(t, err)
		require.Len(t, visible, 1)
		encoded, err := json.Marshal(visible)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "Secret account name")
		cast(3, r.ID, "fail")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		cast(4, r.ID, "fail")
		require.Equal(t, []int64{8, 9}, bindings(), "reject threshold has priority over existing passing votes")
		fresh := begin(p, voting)
		complete(fresh, "pass")
		require.Equal(t, []int64{8, 9}, bindings(), "new votes do not inherit previous passing votes")
		_, err = repo.CastTestVote(ctx, 5, r.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		cast(1, fresh.ID, "pass")
		cast(2, fresh.ID, "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		assertActive(t)
	})

	t.Run("independent scopes and unrelated memberships do not overwrite one another", func(t *testing.T) {
		reset(t)
		exec(`INSERT INTO account_groups(account_id,group_id) VALUES(62,11),(62,13)`)
		first := ruleFor(candyID, []int64{10}, []int64{11}, false)
		second := ruleFor(pelicanID, []int64{12}, []int64{13}, false)
		p := newPlan(first, second)
		complete(begin(p, first), "pass")
		require.Equal(t, []int64{8, 9, 10, 13}, bindings())
		complete(begin(p, second), "fail")
		require.Equal(t, []int64{8, 9, 10, 13}, bindings())
		complete(begin(p, second), "pass")
		require.Equal(t, []int64{8, 9, 10, 12}, bindings())
	})

	t.Run("editing thresholds preserves moved account enrollment until protection is cleared", func(t *testing.T) {
		reset(t)
		rule := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
		p := newPlan(rule)
		complete(begin(p, rule), "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		p.Protection.Rules[0].Thresholds = []service.ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 1000}}
		updated, err := plans.Update(ctx, p)
		require.NoError(t, err)
		ids, err := repo.ListPlanDetectionAccountIDs(ctx, updated, updated.AccountID)
		require.NoError(t, err)
		require.Equal(t, []int64{62}, ids, "threshold edits must not orphan an account outside its source group")
		var states, tracked int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_protection_states WHERE plan_id=$1`, p.ID).Scan(&states))
		require.Zero(t, states, "old thresholds must no longer have a current verdict")
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_managed_accounts WHERE plan_id=$1`, p.ID).Scan(&tracked))
		require.Equal(t, 1, tracked)
		require.NoError(t, repo.ClearPlanProtection(ctx, p.ID))
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_managed_accounts WHERE plan_id=$1`, p.ID).Scan(&tracked))
		require.Zero(t, tracked, "clearing must include enrollments that no longer have a state row")
		ids, err = repo.ListPlanDetectionAccountIDs(ctx, updated, updated.AccountID)
		require.NoError(t, err)
		require.Empty(t, ids)
		require.Equal(t, []int64{9, 10, 11}, bindings(), "clearing protection must not invent a reverse group move")
	})

	t.Run("new rounds require fresh passing results from every type and fence late completions", func(t *testing.T) {
		reset(t)
		candy := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
		pelican := ruleFor(pelicanID, []int64{10, 11}, []int64{8}, false)
		p := newPlan(candy, pelican)
		startRound(p)
		oldCandy := begin(p, candy)
		complete(oldCandy, "fail")
		oldPelican := begin(p, pelican)
		complete(oldPelican, "pass")
		require.Equal(t, []int64{8, 9}, bindings())
		startRound(p)
		complete(oldCandy, "pass")
		var oldVerdict string
		var completed bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT verdict,completed FROM scheduled_test_protection_states WHERE result_id=$1`, oldCandy.ID).Scan(&oldVerdict, &completed))
		require.Equal(t, "pending", oldVerdict, "late completion before this type begins must not reopen an old result")
		require.False(t, completed)
		complete(begin(p, candy), "pass")
		require.Equal(t, []int64{8, 9}, bindings(), "the other type's previous-round pass cannot authorize promotion")
		complete(oldPelican, "pass")
		require.Equal(t, []int64{8, 9}, bindings(), "repeated stale completion cannot satisfy this round")
		complete(begin(p, pelican), "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
	})

	t.Run("preparing a round expires old ballots before its individual test starts", func(t *testing.T) {
		reset(t)
		rule := ruleFor(pelicanID, []int64{10, 11}, []int64{8}, true)
		p := newPlan(rule)
		startRound(p)
		old := begin(p, rule)
		complete(old, "pass")
		cast(1, old.ID, "pass")
		cast(2, old.ID, "pass")
		require.Equal(t, []int64{9, 10, 11}, bindings())
		started := startRound(p)
		_, err := repo.CastTestVote(ctx, 3, old.ID, "fail")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		visible, err := repo.ListVotingResults(ctx, 3)
		require.NoError(t, err)
		require.Empty(t, visible)
		complete(old, "pass")
		_, err = repo.CastTestVote(ctx, 3, old.ID, "fail")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		require.Equal(t, []int64{9, 10, 11}, bindings(), "pending ballots keep the applied tier")
		fresh := begin(p, rule)
		complete(fresh, "pass")
		one := cast(1, fresh.ID, "pass")
		require.Equal(t, 1, one.Voting.PassCount)
		require.NoError(t, repo.BeginProtectionRun(ctx, p, started))
		two := cast(2, fresh.ID, "pass")
		require.Equal(t, 2, two.Voting.PassCount, "repeating preparation for the same round is idempotent")
	})

	t.Run("concurrent plans can enroll their shared account without a foreign key deadlock", func(t *testing.T) {
		reset(t)
		exec(`INSERT INTO account_groups(account_id,group_id) VALUES(62,10),(62,11)`)
		rule := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
		first, second := newPlan(rule), newPlan(rule)
		firstResult, secondResult := begin(first, rule), begin(second, rule)
		for _, result := range []*service.ScheduledTestResult{firstResult, secondResult} {
			result.Status, result.ResponseText = "success", "safe output"
			require.NoError(t, repo.Update(ctx, result))
		}
		concurrentCtx, concurrentCancel := context.WithTimeout(ctx, 8*time.Second)
		defer concurrentCancel()
		blocker, err := db.BeginTx(concurrentCtx, nil)
		require.NoError(t, err)
		defer blocker.Rollback()
		var lockedID int64
		require.NoError(t, blocker.QueryRowContext(concurrentCtx, `SELECT id FROM accounts WHERE id=62 FOR UPDATE`).Scan(&lockedID))
		finished := make(chan error, 2)
		for _, result := range []*service.ScheduledTestResult{firstResult, secondResult} {
			go func(result *service.ScheduledTestResult) {
				finished <- repo.CompleteProtection(concurrentCtx, result, "fail", "quality condition failed")
			}(result)
		}
		// Force both executions to hold their plan locks before either acquires
		// the account. A plan FOR UPDATE lock would then deadlock a managed
		// enrollment's foreign-key lock against the other waiting execution.
		require.Eventually(t, func() bool {
			var blocked int
			err := db.QueryRowContext(concurrentCtx, `SELECT COUNT(*) FROM pg_stat_activity
WHERE application_name=$1 AND wait_event_type='Lock' AND query LIKE '%SELECT status, schedulable, deleted_at FROM accounts%'`, schema).Scan(&blocked)
			return err == nil && blocked == 2
		}, 3*time.Second, 10*time.Millisecond)
		require.NoError(t, blocker.Commit())
		for range 2 {
			select {
			case err := <-finished:
				require.NoError(t, err, "concurrent group outcomes must not deadlock")
			case <-concurrentCtx.Done():
				t.Fatal("concurrent group outcomes did not finish")
			}
		}
		require.Equal(t, []int64{8, 9}, bindings())
		var tracked int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_managed_accounts WHERE account_id=62`).Scan(&tracked))
		require.Equal(t, 2, tracked)
	})

	t.Run("manual scheduling stops block group actions and managed retests", func(t *testing.T) {
		reset(t)
		rule := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
		p := newPlan(rule)
		r := begin(p, rule)
		exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
		complete(r, "pass")
		require.Equal(t, []int64{8, 9}, bindings())
		ids, err := repo.ListPlanDetectionAccountIDs(ctx, p, p.AccountID)
		require.NoError(t, err)
		require.Empty(t, ids)
		var schedulable bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT schedulable FROM accounts WHERE id=62`).Scan(&schedulable))
		require.False(t, schedulable)
	})

	t.Run("outbox carries removed and added groups and commits with memberships", func(t *testing.T) {
		reset(t)
		rule := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
		p := newPlan(rule)
		complete(begin(p, rule), "pass")
		var payload []byte
		require.NoError(t, db.QueryRowContext(ctx, `SELECT payload FROM scheduler_outbox WHERE account_id=62 AND event_type='account_groups_changed' ORDER BY id DESC LIMIT 1`).Scan(&payload))
		var event struct {
			GroupIDs []int64 `json:"group_ids"`
		}
		require.NoError(t, json.Unmarshal(payload, &event))
		require.ElementsMatch(t, []int64{8, 9, 10, 11}, event.GroupIDs)
		r := begin(p, rule)
		exec(`CREATE FUNCTION reject_quality_action_outbox() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN RAISE EXCEPTION 'outbox unavailable'; END $$;
CREATE TRIGGER reject_quality_action_outbox BEFORE INSERT ON scheduler_outbox FOR EACH ROW EXECUTE FUNCTION reject_quality_action_outbox();`)
		err := finish(r, "fail")
		exec(`DROP TRIGGER reject_quality_action_outbox ON scheduler_outbox; DROP FUNCTION reject_quality_action_outbox()`)
		require.Error(t, err)
		require.Equal(t, []int64{9, 10, 11}, bindings(), "failed event persistence must roll back all binding changes")
	})

	for _, mutation := range []struct {
		name, statement string
	}{
		{"foreign platform", `UPDATE groups SET platform='anthropic' WHERE id=10`},
		{"deleted group", `UPDATE groups SET deleted_at=NOW() WHERE id=10`},
		{"inactive group", `UPDATE groups SET status='disabled' WHERE id=10`},
	} {
		t.Run("reject "+mutation.name+" at configuration and action time", func(t *testing.T) {
			reset(t)
			rule := ruleFor(candyID, []int64{10, 11}, []int64{8}, false)
			exec(mutation.statement)
			_, err := plans.Create(ctx, planInput(rule))
			require.Error(t, err)
			require.Equal(t, []int64{8, 9}, bindings())
			exec(`UPDATE groups SET platform='openai',status='active',deleted_at=NULL WHERE id=10`)
			p := newPlan(rule)
			r := begin(p, rule)
			exec(mutation.statement)
			require.Error(t, finish(r, "pass"))
			require.Equal(t, []int64{8, 9}, bindings(), "a stale action target must never remove existing bindings")
			var count int
			require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=62`).Scan(&count))
			require.Zero(t, count)
		})
	}
}
