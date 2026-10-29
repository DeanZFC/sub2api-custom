package service

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

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
	var upstreamCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		upstreamCalls.Add(1)
		return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"}, Extra: map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	runner.automaticRetryBaseDelay = time.Millisecond
	upstream := &modelIdentityHTTPUpstream{}
	upstream.beforeReturn = func(_ *http.Request, _ int) {
		if upstream.requestModel == "model-a" {
			upstream.responses = []*http.Response{costJSONResponse(400, "invalid model")}
		} else {
			upstream.responses = []*http.Response{costJSONResponse(200, `{"model":"model-b","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"PASS"}]}]}`)}
		}
	}
	runner.accountTestSvc.httpUpstream = upstream
	runner.accountTestSvc.cfg = &config.Config{}
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	plan.ModelID, plan.ModelIDs = "model-a", []string{"model-a", "model-b"}
	plan.TestDefinitionIDs = []int64{1, 2}
	runner.RunPlanNow(context.Background(), plan)
	require.Len(t, <-repo.published, 4, "freeze each account/type/model before execution")
	close(repo.updated)
	finals := map[string]*ScheduledTestResult{}
	for result := range repo.updated {
		if result.Status != "running" {
			finals[fmt.Sprintf("%s/%d", result.ModelID, *result.TestDefinitionID)] = result
		}
	}
	require.Len(t, finals, 4)
	for _, id := range plan.TestDefinitionIDs {
		require.Equal(t, "failed", finals[fmt.Sprintf("model-a/%d", id)].Status)
		require.Equal(t, "success", finals[fmt.Sprintf("model-b/%d", id)].Status, finals[fmt.Sprintf("model-b/%d", id)].ErrorMessage)
	}
	require.GreaterOrEqual(t, upstreamCalls.Load(), int32(3), "active failure of model-a must not suppress model-b checks")
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
