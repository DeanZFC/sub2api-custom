package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestMultipleModelsValidationAndExecutionOrder(t *testing.T) {
	plan := strategyPlan()
	plan.ReasoningEffort = ""
	plan.ModelIDs = []string{" model-a ", "model-b", "model-a"}
	require.NoError(t, validateScheduledTestPlan(plan))
	require.Equal(t, []string{"model-a", "model-b"}, plan.ModelIDs)
	require.Equal(t, "model-a", plan.ModelID)
	executions := scheduledTestExecutionPlans(plan)
	require.Len(t, executions, 6)
	for i, execution := range executions {
		require.Equal(t, plan.ModelIDs[i/3], execution.ModelID)
		require.Equal(t, plan.TestDefinitionIDs[i%3], *execution.TestDefinitionID)
	}
	for _, models := range [][]string{{}, {" "}, make([]string, 17)} {
		plan.ModelIDs = models
		require.Error(t, validateScheduledTestPlan(plan))
	}
	plan.ModelIDs = []string{"gpt-6-astra", "gpt-4o"}
	plan.ReasoningEffort = "high"
	require.ErrorContains(t, validateScheduledTestPlan(plan), "gpt-4o")
}

func TestScheduledTestMultipleModelsSnapshotFailureIsIsolated(t *testing.T) {
	repo := &snapshotResultRepoStub{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 30)},
		targets:             []int64{12}, published: make(chan []*ScheduledTestResult, 1),
	}
	repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
		if filter.Model == "model-a" {
			return nil, errors.New("model-a statistics unavailable")
		}
		return &ScheduledTestStatistics{TotalRequests: 10, SuccessRequests: 10}, nil
	}
	var upstreamCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		upstreamCalls.Add(1)
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a", "model-b"}
	plan.TestDefinitionIDs = []int64{1, 2, 3}
	runner.RunPlanNow(context.Background(), plan)
	require.Len(t, <-repo.published, 6, "freeze each account/type/model before execution")
	close(repo.updated)
	finals := map[string]*ScheduledTestResult{}
	for result := range repo.updated {
		if result.Status != "running" {
			finals[fmt.Sprintf("%s/%d", result.ModelID, *result.TestDefinitionID)] = result
		}
	}
	require.Len(t, finals, 6)
	for _, id := range plan.TestDefinitionIDs {
		require.Equal(t, "failed", finals[fmt.Sprintf("model-a/%d", id)].Status)
		require.Equal(t, "success", finals[fmt.Sprintf("model-b/%d", id)].Status)
	}
	require.EqualValues(t, 2, upstreamCalls.Load(), "failure of model-a must not suppress model-b's checks")
}

func TestScheduledTestRetryAccountGuardIsModelScoped(t *testing.T) {
	runner := &ScheduledTestRunnerService{}
	id := int64(1)
	require.True(t, runner.beginAccountRun(1, 2, &id, "model-a"))
	require.False(t, runner.beginAccountRun(1, 2, &id, "model-a"))
	require.True(t, runner.beginAccountRun(1, 2, &id, "model-b"))
	runner.endAccountRun(1, 2, &id, "model-a")
	require.True(t, runner.beginAccountRun(1, 2, &id, "model-a"))
	require.False(t, runner.beginAccountRun(1, 2, &id, "model-b"))
}

func TestScheduledTestRetryRejectsRemovedModel(t *testing.T) {
	accountID := int64(2)
	runner := &ScheduledTestRunnerService{}
	plan := &ScheduledTestPlan{ID: 1, ModelID: "model-a", ModelIDs: []string{"model-a", "model-b"}}
	_, err := runner.RetryAccount(context.Background(), plan, &ScheduledTestResult{ID: 3, PlanID: 1, AccountID: &accountID, Status: "failed", ModelID: "removed-model"})
	require.ErrorContains(t, err, "no longer selected")
}
