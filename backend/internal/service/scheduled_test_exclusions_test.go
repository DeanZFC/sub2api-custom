package service

import (
	"context"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestExclusionsSkipAllChecksAndRetries(t *testing.T) {
	repo := &snapshotResultRepoStub{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 16)},
		targets:             []int64{12, 13, 12},
	}
	var excludedCalls, includedCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		if id == 12 {
			excludedCalls.Add(1)
		} else {
			includedCalls.Add(1)
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	plan.TestDefinitionIDs = []int64{1, 2}
	plan.ExcludedAccountIDs = []int64{12}
	runner.RunPlanNow(context.Background(), plan)
	close(repo.updated)
	for result := range repo.updated {
		require.NotNil(t, result.AccountID)
		require.EqualValues(t, 13, *result.AccountID)
	}
	require.Equal(t, map[int64]bool{13: true}, repo.frozenMembers)
	require.Zero(t, excludedCalls.Load())
	require.Positive(t, includedCalls.Load())
	previous := &ScheduledTestResult{ID: 100, PlanID: plan.ID, AccountID: scheduledTestPtrInt64(12), Status: "failed", RunID: repo.latestRun}
	_, err := runner.RetryAccount(context.Background(), plan, previous)
	require.ErrorContains(t, err, "not enabled for detection")
}
