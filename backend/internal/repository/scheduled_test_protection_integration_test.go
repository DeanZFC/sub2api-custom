//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

type protectionRunnerStatisticsRepo struct {
	*scheduledTestResultRepository
	cacheRate float64
	requests  int64
	calls     atomic.Int32
}

func (r *protectionRunnerStatisticsRepo) CollectStatistics(_ context.Context, filter service.ScheduledTestStatisticsFilter) (*service.ScheduledTestStatistics, error) {
	r.calls.Add(1)
	rate := r.cacheRate
	return &service.ScheduledTestStatistics{
		WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd,
		TotalRequests: r.requests, SuccessRequests: r.requests,
		CacheInputTokens: r.requests * 100, CacheReadTokens: int64(float64(r.requests*100) * rate), CacheRate: &rate,
	}, nil
}

func TestScheduledTestProtectionIntegration(t *testing.T) {
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	defer admin.Close()
	schema := fmt.Sprintf("quality_protection_%d", time.Now().UnixNano())
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
	exec(`CREATE TABLE users (id BIGINT PRIMARY KEY);
CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,status TEXT NOT NULL DEFAULT 'active',schedulable BOOLEAN NOT NULL DEFAULT TRUE,extra JSONB DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT NOW(),deleted_at TIMESTAMPTZ);
CREATE TABLE groups(id BIGINT PRIMARY KEY,name TEXT,status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ,is_exclusive BOOLEAN DEFAULT FALSE,subscription_type TEXT DEFAULT 'standard');
CREATE TABLE account_groups(account_id BIGINT,group_id BIGINT);
CREATE TABLE user_allowed_groups(user_id BIGINT,group_id BIGINT);
CREATE TABLE user_subscriptions(user_id BIGINT,group_id BIGINT,deleted_at TIMESTAMPTZ,status TEXT,starts_at TIMESTAMPTZ,expires_at TIMESTAMPTZ);
CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT NOW());
CREATE UNIQUE INDEX idx_protection_outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
INSERT INTO users SELECT generate_series(1,20);
INSERT INTO accounts(id,name) VALUES(62,'Secret account name'),(63,'Other account');
INSERT INTO groups(id,name,is_exclusive) VALUES(8,'Public group',FALSE),(9,'Private group',TRUE);
INSERT INTO account_groups VALUES(62,8),(62,9),(63,8);`)
	for _, name := range []string{
		"066_add_scheduled_test_tables.sql", "070_add_scheduled_test_auto_recover.sql",
		"247_generalized_scheduled_tests.sql", "248_scheduled_test_reasoning_effort.sql", "249_scheduled_test_result_reasoning_effort.sql",
		"250_allow_group_account_scheduled_test_targets.sql", "251_scheduled_test_target_modes.sql", "252_scheduled_test_definition_sort_order.sql",
		"253_scheduled_test_plan_sort_order.sql", "256_scheduled_test_multiple_definitions.sql", "257_scheduled_test_hourly_statistics.sql",
		"258_scheduled_test_protection.sql", "259_scheduled_test_outcome_actions.sql",
		"261_scheduled_test_admin_review.sql",
	} {
		raw, err := migrations.FS.ReadFile(name)
		require.NoError(t, err)
		exec(string(raw))
	}
	var candyID, htmlID int64
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='candy'`).Scan(&candyID))
	require.NoError(t, db.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='pelican'`).Scan(&htmlID))
	plans := &scheduledTestPlanRepository{db: db}
	repo := &scheduledTestResultRepository{db: db}
	groupID, accountID := int64(8), int64(62)
	plain := service.ScheduledTestProtectionRule{TestDefinitionID: candyID, PauseOnFailure: true}
	voting := service.ScheduledTestProtectionRule{TestDefinitionID: htmlID, ExpectedAnswer: "reference-only", Vote: &service.ScheduledTestVoteConfig{Enabled: true, RejectAbove: 1, PassAtLeast: 2}}
	reset := func() {
		exec(`TRUNCATE scheduled_test_plans,scheduled_test_results,scheduled_test_plan_definitions,scheduled_test_protection_states,scheduled_test_votes,scheduler_outbox RESTART IDENTITY CASCADE`)
		exec(`UPDATE accounts SET status='active',schedulable=TRUE,deleted_at=NULL,extra='{}';TRUNCATE user_allowed_groups,user_subscriptions`)
	}
	newPlan := func(rules ...service.ScheduledTestProtectionRule) *service.ScheduledTestPlan {
		t.Helper()
		ids := make([]int64, 0, len(rules))
		for _, rule := range rules {
			ids = append(ids, rule.TestDefinitionID)
		}
		p, err := plans.Create(ctx, &service.ScheduledTestPlan{Name: "Quality rule", GroupID: &groupID, AccountID: &accountID, TargetMode: "account", TestDefinitionID: &ids[0], TestDefinitionIDs: ids, ModelID: "model", CronExpression: "* * * * *", Enabled: true, MaxResults: 3, Protection: service.ScheduledTestProtectionConfig{Enabled: true, Rules: rules}})
		require.NoError(t, err)
		return p
	}
	sequence := int64(0)
	newResult := func(plan *service.ScheduledTestPlan, definition int64) *service.ScheduledTestResult {
		t.Helper()
		sequence++
		started := time.Now().UTC().Add(time.Duration(sequence) * time.Second).Truncate(time.Microsecond)
		result, err := repo.Create(ctx, &service.ScheduledTestResult{PlanID: plan.ID, TestDefinitionID: &definition, GroupID: plan.GroupID, AccountID: plan.AccountID, TargetMode: plan.TargetMode, ModelID: plan.ModelID, ReasoningEffort: plan.ReasoningEffort, Status: "running", OutputKind: "html", StartedAt: started, FinishedAt: started})
		require.NoError(t, err)
		return result
	}
	begin := func(plan *service.ScheduledTestPlan, rule service.ScheduledTestProtectionRule) *service.ScheduledTestResult {
		t.Helper()
		result := newResult(plan, rule.TestDefinitionID)
		require.NoError(t, repo.BeginProtection(ctx, result, rule))
		return result
	}
	complete := func(result *service.ScheduledTestResult, verdict string) {
		t.Helper()
		result.Status = "success"
		result.ResponseText = "safe output"
		result.OutputHTML = "<p>safe output</p>"
		require.NoError(t, repo.Update(ctx, result))
		require.NoError(t, repo.CompleteProtection(ctx, result, verdict, "quality failed"))
	}
	status := func() string {
		t.Helper()
		var got string
		require.NoError(t, db.QueryRowContext(ctx, `SELECT status FROM accounts WHERE id=62`).Scan(&got))
		return got
	}
	cast := func(userID, resultID int64, vote string) *service.ScheduledTestVoteResult {
		t.Helper()
		result, err := repo.CastTestVote(ctx, userID, resultID, vote)
		require.NoError(t, err)
		return result
	}

	t.Run("detection includes automatic pauses but never manual switches or other failures", func(t *testing.T) {
		reset()
		exec(`UPDATE accounts SET status='quality_paused' WHERE id=62;UPDATE accounts SET schedulable=FALSE WHERE id=63`)
		ids, err := repo.ListDetectionAccountIDs(ctx, &groupID, nil)
		require.NoError(t, err)
		require.Equal(t, []int64{62}, ids)
		for _, state := range []string{"error", "disabled"} {
			exec(`UPDATE accounts SET status=$1 WHERE id=62`, state)
			ids, err = repo.ListDetectionAccountIDs(ctx, &groupID, &accountID)
			require.NoError(t, err)
			require.Empty(t, ids)
		}
		exec(`UPDATE accounts SET status='active',deleted_at=NOW() WHERE id=62`)
		ids, err = repo.ListDetectionAccountIDs(ctx, nil, &accountID)
		require.NoError(t, err)
		require.Empty(t, ids)
	})
	t.Run("every rule hold must pass and new pending rounds retain holds", func(t *testing.T) {
		reset()
		second := plain
		second.TestDefinitionID = htmlID
		p := newPlan(plain, second)
		first := begin(p, plain)
		complete(first, "fail")
		require.Equal(t, "quality_paused", status())
		other := begin(p, second)
		complete(other, "fail")
		newFirst := begin(p, plain)
		require.Equal(t, "quality_paused", status())
		require.NoError(t, repo.CompleteProtection(ctx, first, "pass", ""))
		require.Equal(t, "quality_paused", status(), "old completion cannot release a newer round")
		complete(newFirst, "pass")
		require.Equal(t, "quality_paused", status(), "a different failed rule still holds the account")
		newOther := begin(p, second)
		complete(newOther, "pending")
		require.Equal(t, "quality_paused", status(), "insufficient samples cannot release a hold")
		last := begin(p, second)
		complete(last, "pass")
		require.Equal(t, "active", status())
		var schedulable bool
		var reason sql.NullString
		var events int
		require.NoError(t, db.QueryRowContext(ctx, `SELECT schedulable,extra->>'quality_protection_reason' FROM accounts WHERE id=62`).Scan(&schedulable, &reason))
		require.True(t, schedulable)
		require.False(t, reason.Valid)
		require.NoError(t, db.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduler_outbox WHERE account_id=62`).Scan(&events))
		require.Positive(t, events)
	})
	t.Run("votes use strict reject threshold allow changes and require passes to recover", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		r := begin(p, voting)
		complete(r, "pass")
		one := cast(1, r.ID, "fail")
		require.Equal(t, 1, one.Voting.FailCount)
		require.Equal(t, "active", status())
		two := cast(2, r.ID, "fail")
		require.True(t, two.Voting.AccountPaused)
		require.Equal(t, "quality_paused", status())
		changed := cast(1, r.ID, "pass")
		require.Equal(t, 1, changed.Voting.FailCount)
		require.Equal(t, 1, changed.Voting.PassCount)
		require.Equal(t, "quality_paused", status(), "pending votes retain the previous hold")
		recovered := cast(3, r.ID, "pass")
		require.Equal(t, 2, recovered.Voting.PassCount)
		require.Equal(t, "active", status())
		require.Equal(t, "pass", recovered.Voting.MyVote)
		cast(3, r.ID, "fail")
		require.Equal(t, "quality_paused", status(), "reject votes win even if enough older passes exist")
		visible, err := repo.ListVisible(ctx, 1, 3)
		require.NoError(t, err)
		require.Empty(t, visible, "ordinary results stay hidden for paused accounts")
		votes, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Len(t, votes, 1)
		require.True(t, votes[0].Voting.Open)
		require.Equal(t, "reference-only", votes[0].Voting.ReferenceAnswer)
		encoded, err := json.Marshal(votes)
		require.NoError(t, err)
		require.NotContains(t, string(encoded), "Secret account name")
		require.NotContains(t, string(encoded), "account_name")
		require.NotContains(t, string(encoded), "credentials")
	})
	t.Run("latest round invalidates old ballots and auto failures cannot be voted away", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		old := begin(p, voting)
		complete(old, "pass")
		cast(1, old.ID, "fail")
		cast(2, old.ID, "fail")
		current := begin(p, voting)
		require.Equal(t, "quality_paused", status())
		_, err := repo.CastTestVote(ctx, 3, old.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		list, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, list, "an unfinished round must not offer voting")
		complete(current, "pass")
		require.Equal(t, "quality_paused", status(), "a successful generated result still needs new pass votes")
		first := cast(1, current.ID, "pass")
		require.Equal(t, 1, first.Voting.PassCount)
		require.Zero(t, first.Voting.FailCount)
		require.Equal(t, "quality_paused", status())
		cast(2, current.ID, "pass")
		require.Equal(t, "active", status())
		failed := begin(p, voting)
		complete(failed, "fail")
		require.Equal(t, "quality_paused", status())
		_, err = repo.CastTestVote(ctx, 1, failed.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		list, err = repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.False(t, list[0].Voting.Open)
	})
	t.Run("private groups and current manual account state are enforced at vote time", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		private := int64(9)
		p.GroupID = &private
		p, err = plans.Update(ctx, p)
		require.NoError(t, err)
		r := begin(p, voting)
		complete(r, "pass")
		list, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, list)
		_, err = repo.CastTestVote(ctx, 1, r.ID, "fail")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		exec(`INSERT INTO user_allowed_groups VALUES(2,9);INSERT INTO user_subscriptions VALUES(3,9,NULL,'active',NOW()-INTERVAL '1 day',NOW()+INTERVAL '1 day'),(4,9,NULL,'active',NOW()-INTERVAL '2 day',NOW()-INTERVAL '1 day')`)
		cast(2, r.ID, "fail")
		cast(3, r.ID, "fail")
		require.Equal(t, "quality_paused", status())
		_, err = repo.CastTestVote(ctx, 4, r.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
		_, err = repo.CastTestVote(ctx, 2, r.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		require.NoError(t, repo.CompleteProtection(ctx, r, "pass", ""))
		require.Equal(t, "quality_paused", status())
		list, err = repo.ListVotingResults(ctx, 2)
		require.NoError(t, err)
		require.Empty(t, list)
		require.NoError(t, repo.ClearPlanProtection(ctx, p.ID))
		require.Equal(t, "quality_paused", status(), "clearing must not undo a manual scheduling switch")
		var schedulable bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT schedulable FROM accounts WHERE id=62`).Scan(&schedulable))
		require.False(t, schedulable)
	})
	t.Run("metadata edits preserve votes while config edits atomically release only their holds", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		r := begin(p, voting)
		complete(r, "pass")
		cast(1, r.ID, "fail")
		cast(2, r.ID, "fail")
		p.Name = "renamed"
		p.CronExpression = "*/5 * * * *"
		p, err = plans.Update(ctx, p)
		require.NoError(t, err)
		require.Equal(t, "quality_paused", status())
		list, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, 2, list[0].Voting.FailCount)
		other := newPlan(plain)
		otherResult := begin(other, plain)
		complete(otherResult, "fail")
		p.Enabled = false
		p, err = plans.Update(ctx, p)
		require.NoError(t, err)
		require.Equal(t, "quality_paused", status(), "another plan still blocks")
		require.NoError(t, repo.CompleteProtection(ctx, r, "fail", "late completion"))
		require.NoError(t, plans.Delete(ctx, other.ID))
		require.Equal(t, "active", status())
		require.NoError(t, repo.BeginProtection(ctx, r, voting))
		require.NoError(t, repo.CompleteProtection(ctx, r, "fail", "late completion"))
		require.Equal(t, "active", status(), "disabled plan cannot recreate holds")
	})
	t.Run("target edits reject late Begin and deleting a result retains its hold", func(t *testing.T) {
		reset()
		p := newPlan(plain)
		r := begin(p, plain)
		complete(r, "fail")
		p.ModelID = "new-model"
		p, err = plans.Update(ctx, p)
		require.NoError(t, err)
		require.Equal(t, "active", status())
		require.NoError(t, repo.BeginProtection(ctx, r, plain))
		require.NoError(t, repo.CompleteProtection(ctx, r, "fail", "old model"))
		require.Equal(t, "active", status())
		current := begin(p, plain)
		complete(current, "fail")
		require.Equal(t, "quality_paused", status())
		require.NoError(t, repo.Delete(ctx, current.ID))
		require.Equal(t, "quality_paused", status(), "result deletion must not implicitly approve a failed account")
		newer := begin(p, plain)
		complete(newer, "pass")
		require.Equal(t, "active", status())
	})
	t.Run("pruning retains the active protection round even when newer unprotected results exist", func(t *testing.T) {
		reset()
		p := newPlan(plain)
		r := begin(p, plain)
		complete(r, "fail")
		for i := 0; i < 5; i++ {
			other := newResult(p, plain.TestDefinitionID)
			other.Status = "success"
			require.NoError(t, repo.Update(ctx, other))
		}
		require.NoError(t, repo.PruneOldResults(ctx, p.ID, 1))
		_, err := repo.GetByID(ctx, r.ID)
		require.NoError(t, err)
		require.Equal(t, "quality_paused", status())
	})
	t.Run("same result retry increments generation and removes its previous votes", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		r := begin(p, voting)
		complete(r, "pass")
		cast(1, r.ID, "fail")
		cast(2, r.ID, "fail")
		r.Status = "failed"
		require.NoError(t, repo.Update(ctx, r))
		r.StartedAt = r.StartedAt.Add(time.Minute + 789*time.Nanosecond)
		r.FinishedAt = r.StartedAt
		r.Status = "running"
		require.NoError(t, repo.RestartFailed(ctx, r))
		require.NoError(t, repo.BeginProtection(ctx, r, voting))
		complete(r, "pass")
		list, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Zero(t, list[0].Voting.FailCount)
		require.Empty(t, list[0].Voting.MyVote)
		require.Equal(t, "quality_paused", status())
		cast(1, r.ID, "pass")
		cast(2, r.ID, "pass")
		require.Equal(t, "active", status())
	})
	t.Run("failed outputs cannot be voted on and recovery never overwrites another account failure", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		r := begin(p, voting)
		r.Status = "failed"
		r.ErrorMessage = "private upstream error"
		require.NoError(t, repo.Update(ctx, r))
		require.NoError(t, repo.CompleteProtection(ctx, r, "pending", ""))
		list, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Empty(t, list)
		_, err = repo.CastTestVote(ctx, 1, r.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
		_, err = repo.CastTestVote(ctx, 1, r.ID, "invalid")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteInvalid)
		r = begin(p, voting)
		complete(r, "pass")
		cast(1, r.ID, "fail")
		cast(2, r.ID, "fail")
		exec(`UPDATE accounts SET status='error' WHERE id=62`)
		require.NoError(t, repo.ClearPlanProtection(ctx, p.ID))
		require.Equal(t, "error", status())
		_, err = repo.CastTestVote(ctx, 1, r.ID, "pass")
		require.ErrorIs(t, err, service.ErrScheduledTestVoteUnavailable)
	})
	t.Run("runner persists metrics and pauses then retests and recovers without enabling manual stops", func(t *testing.T) {
		reset()
		definitions := NewScheduledTestDefinitionRepository(db)
		stats, err := definitions.GetByKey(ctx, "hourly_stats")
		require.NoError(t, err)
		rule := service.ScheduledTestProtectionRule{TestDefinitionID: stats.ID, MinSamples: 10, Thresholds: []service.ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 80}}}
		p := newPlan(rule)
		results := &protectionRunnerStatisticsRepo{scheduledTestResultRepository: repo, cacheRate: .5, requests: 20}
		svc := service.NewScheduledTestService(plans, results)
		svc.SetDefinitionRepository(definitions)
		runner := service.NewScheduledTestRunnerService(plans, svc, nil, nil, nil, nil)
		defer runner.Stop()
		runner.RunPlanNow(ctx, p)
		require.Equal(t, "quality_paused", status())
		var resultID int64
		var blocked, completed bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT result_id,blocked,completed FROM scheduled_test_protection_states WHERE plan_id=$1`, p.ID).Scan(&resultID, &blocked, &completed))
		require.True(t, blocked)
		require.True(t, completed, "nanosecond timestamps must not lose protection completion")
		stored, err := repo.GetByID(ctx, resultID)
		require.NoError(t, err)
		require.Equal(t, "success", stored.Status)

		results.requests = 0
		runner.RunPlanNow(ctx, p)
		require.Equal(t, "quality_paused", status(), "empty traffic must not release a hold")
		results.requests, results.cacheRate = 20, .95
		runner.RunPlanNow(ctx, p)
		require.Equal(t, "active", status())
		require.Equal(t, int32(3), results.calls.Load())
		exec(`UPDATE accounts SET schedulable=FALSE WHERE id=62`)
		runner.RunPlanNow(ctx, p)
		require.Equal(t, int32(3), results.calls.Load(), "manual stops must never execute statistics for the account")
		var schedulable bool
		require.NoError(t, db.QueryRowContext(ctx, `SELECT schedulable FROM accounts WHERE id=62`).Scan(&schedulable))
		require.False(t, schedulable)
	})
	t.Run("concurrent votes are unique and threshold checks are serialized", func(t *testing.T) {
		reset()
		p := newPlan(voting)
		r := begin(p, voting)
		complete(r, "pass")
		var wg sync.WaitGroup
		errs := make(chan error, 12)
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				vote := "pass"
				if i%2 == 0 {
					vote = "fail"
				}
				_, err := repo.CastTestVote(ctx, int64(i%3+1), r.ID, vote)
				errs <- err
			}(i)
		}
		wg.Wait()
		close(errs)
		for err := range errs {
			require.NoError(t, err)
		}
		list, err := repo.ListVotingResults(ctx, 1)
		require.NoError(t, err)
		require.Len(t, list, 1)
		require.Equal(t, 3, list[0].Voting.PassCount+list[0].Voting.FailCount)
		if list[0].Voting.FailCount > 1 {
			require.Equal(t, "quality_paused", status())
		} else if list[0].Voting.PassCount >= 2 {
			require.Equal(t, "active", status())
		}
	})
}
