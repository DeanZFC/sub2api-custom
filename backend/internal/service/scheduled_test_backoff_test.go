package service

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type costBackoffRepo struct {
	*snapshotResultRepoStub
	mu     sync.Mutex
	states map[int64]*ScheduledTestBackoff
	policy map[int64]string
	writes int
}

func (r *costBackoffRepo) GetTestBackoff(_ context.Context, _ int64, id int64, key string) (*ScheduledTestBackoff, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.policy[id] != key {
		return nil, nil
	}
	return r.states[id], nil
}
func (r *costBackoffRepo) RecordTestBackoff(_ context.Context, _ *ScheduledTestPlan, result *ScheduledTestResult, key string, failed bool, retryAfter *time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	id := *result.AccountID
	count := 0
	if old := r.states[id]; old != nil && r.policy[id] == key {
		count = old.ConsecutiveFailures
	}
	if failed {
		count++
	} else {
		count = 0
	}
	next := time.Now()
	if failed {
		next = next.Add(ScheduledTestBackoffDelay(count))
		if retryAfter != nil && retryAfter.After(next) {
			next = *retryAfter
		}
	}
	r.states[id] = &ScheduledTestBackoff{ConsecutiveFailures: count, NextCheckAt: next}
	r.policy[id] = key
	r.writes++
	return nil
}
func TestScheduledTestCostBackoffIsPerAccountAndManualBypasses(t *testing.T) {
	base := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{}, targets: []int64{12, 13}}
	repo := &costBackoffRepo{snapshotResultRepoStub: base, states: map[int64]*ScheduledTestBackoff{}, policy: map[int64]string{}}
	var mu sync.Mutex
	calls := map[int64]int{}
	unhealthy := true
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		mu.Lock()
		defer mu.Unlock()
		calls[id]++
		if id == 12 && unhealthy {
			return nil, errors.New("missing account")
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(base, accounts)
	runner.scheduledSvc.resultRepo = repo
	defer runner.Stop()
	plan := automaticRetryTestPlan()
	plan.TestDefinitionIDs = []int64{1, 2}
	automatic := context.WithValue(context.Background(), scheduledTestAutomaticRunKey{}, true)
	runner.executePlanSnapshot(automatic, plan, repo)
	require.Equal(t, 1, calls[12])
	require.Equal(t, 2, calls[13])
	require.Equal(t, 1, repo.states[12].ConsecutiveFailures)
	require.WithinDuration(t, time.Now().Add(5*time.Minute), repo.states[12].NextCheckAt, time.Second)
	firstWrites := repo.writes
	runner.executePlanSnapshot(automatic, plan, repo)
	require.Equal(t, 1, calls[12], "cooling account must make no paid requests")
	require.Equal(t, 4, calls[13], "other accounts keep running")
	require.Equal(t, firstWrites+1, repo.writes, "skips must not extend cooldown or failure streak")
	unhealthy = false
	runner.executePlanSnapshot(context.Background(), plan, repo)
	require.Equal(t, 3, calls[12], "manual full run bypasses cooldown and performs every check")
	require.Zero(t, repo.states[12].ConsecutiveFailures)
	runner.executePlanSnapshot(automatic, plan, repo)
	require.Equal(t, 5, calls[12], "successful recovery resets cooldown")
	// A model change ignores cooldown established by the old execution policy.
	unhealthy = true
	runner.executePlanSnapshot(automatic, plan, repo)
	require.Equal(t, 6, calls[12])
	plan.ModelID = "changed-model"
	runner.executePlanSnapshot(automatic, plan, repo)
	require.Equal(t, 7, calls[12])
	require.Equal(t, 1, repo.states[12].ConsecutiveFailures)
}
func TestScheduledTestCostBackoffStepsAndNoSamples(t *testing.T) {
	for i, minutes := range []int{5, 15, 30, 60, 60, 60} {
		require.Equal(t, time.Duration(minutes)*time.Minute, ScheduledTestBackoffDelay(i+1))
	}
	repo := &costBackoffRepo{snapshotResultRepoStub: &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{}}, states: map[int64]*ScheduledTestBackoff{}, policy: map[int64]string{}}
	runner := newSnapshotRunner(repo.snapshotResultRepoStub, nil)
	runner.scheduledSvc.resultRepo = repo
	defer runner.Stop()
	plan := automaticRetryTestPlan()
	id := int64(12)
	queue := []*scheduledTestExecutionTarget{{plan: plan, accountID: &id, kind: "statistics"}}
	result := &ScheduledTestResult{AccountID: &id, Status: "success", OutputKind: "statistics", OutputStatistics: &ScheduledTestStatistics{}}
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", []*ScheduledTestResult{result}, false, true)
	require.Zero(t, repo.writes, "no samples does not claim recovery")
	result.Status = "failed"
	result.OutputStatistics = nil
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", []*ScheduledTestResult{result}, true, true)
	require.Zero(t, repo.writes, "database failures do not penalize the account")
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", []*ScheduledTestResult{nil}, true, true)
	require.Zero(t, repo.writes, "skipped or failed persistence is not quality evidence")
	plan.TestDefinitionID = scheduledTestPtrInt64(1)
	plan.Enabled = true
	plan.Protection = ScheduledTestProtectionConfig{Enabled: true, Mode: "all_pass", Rules: []ScheduledTestProtectionRule{{
		TestDefinitionID: 1, MinSamples: 10,
		Thresholds: []ScheduledTestThreshold{{Metric: "success_rate", Operator: "lt", Value: 95}},
	}}}
	rate := 1.0
	result.Status = "success"
	result.OutputStatistics = &ScheduledTestStatistics{TotalRequests: 3, SuccessRate: &rate}
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", []*ScheduledTestResult{result}, false, true)
	require.Zero(t, repo.writes, "insufficient samples cannot reset an existing failure streak")
	result.OutputStatistics.TotalRequests = 10
	rate = 0.5
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", []*ScheduledTestResult{result}, true, false)
	require.Equal(t, 1, repo.states[id].ConsecutiveFailures, "a measured quality failure increases cooldown even when execution succeeded")
}

func TestScheduledTestCostBackoffRespectsPassingORBranch(t *testing.T) {
	repo := &costBackoffRepo{snapshotResultRepoStub: &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{}}, states: map[int64]*ScheduledTestBackoff{}, policy: map[int64]string{}}
	runner := newSnapshotRunner(repo.snapshotResultRepoStub, nil)
	runner.scheduledSvc.resultRepo = repo
	defer runner.Stop()
	plan := automaticRetryTestPlan()
	plan.Enabled = true
	plan.Protection = ScheduledTestProtectionConfig{Enabled: true, Mode: "combined", Rules: []ScheduledTestProtectionRule{
		{TestDefinitionID: 1, ExpectedAnswer: "expected"}, {TestDefinitionID: 2},
	}, Combinations: []ScheduledTestCombinationRule{{Condition: ScheduledTestCombinationCondition{Operator: "any", Conditions: []ScheduledTestCombinationCondition{
		{Operator: "test", TestDefinitionID: 1, Verdict: "pass"}, {Operator: "test", TestDefinitionID: 2, Verdict: "pass"},
	}}}}}
	second := *plan
	plan.TestDefinitionID, second.TestDefinitionID = scheduledTestPtrInt64(1), scheduledTestPtrInt64(2)
	id := int64(12)
	queue := []*scheduledTestExecutionTarget{{plan: plan, accountID: &id}, {plan: &second, accountID: &id}}
	observations := []*ScheduledTestResult{{AccountID: &id, Status: "success", ResponseText: "wrong"}, {AccountID: &id, Status: "success"}}
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", observations, false, true)
	require.Equal(t, 1, repo.writes)
	require.Zero(t, repo.states[id].ConsecutiveFailures, "a matching combined condition is healthy under the configured policy")

	plan.Protection.Combinations = []ScheduledTestCombinationRule{{ID: "failure", Condition: ScheduledTestCombinationCondition{Operator: "test", TestDefinitionID: 1, Verdict: "fail"}}}
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", observations, false, true)
	require.Equal(t, 1, repo.states[id].ConsecutiveFailures, "a matching failure branch must not claim health")
	plan.Protection.Combinations = append(plan.Protection.Combinations, ScheduledTestCombinationRule{ID: "positive", Priority: 1, Condition: ScheduledTestCombinationCondition{Operator: "test", TestDefinitionID: 2, Verdict: "pass"}, Action: ScheduledTestOutcomeAction{Scheduling: "pause"}})
	runner.recordAccountQueueBackoff(context.Background(), queue, "policy", observations, false, true)
	require.Equal(t, 2, repo.states[id].ConsecutiveFailures, "the selected pause must not be bypassed by a matching positive condition")
}
