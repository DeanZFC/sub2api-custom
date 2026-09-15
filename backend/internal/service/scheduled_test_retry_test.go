package service

import (
	"context"
	"database/sql"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type retryPlanRepoStub struct {
	ScheduledTestPlanRepository
	plan *ScheduledTestPlan
}

func (r retryPlanRepoStub) GetByID(context.Context, int64) (*ScheduledTestPlan, error) {
	if r.plan == nil {
		return nil, sql.ErrNoRows
	}
	return r.plan, nil
}

type retryResultRepoStub struct {
	ScheduledTestResultRepository
	previous *ScheduledTestResult
	mu       sync.Mutex
	created  []*ScheduledTestResult
	updated  chan *ScheduledTestResult
}

func (r *retryResultRepoStub) GetByID(context.Context, int64) (*ScheduledTestResult, error) {
	if r.previous == nil {
		return nil, sql.ErrNoRows
	}
	return r.previous, nil
}

func (r *retryResultRepoStub) Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	copy := *result
	copy.ID = int64(100 + len(r.created))
	r.created = append(r.created, &copy)
	return &copy, nil
}

func (r *retryResultRepoStub) Update(ctx context.Context, result *ScheduledTestResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	copy := *result
	if r.updated != nil {
		r.updated <- &copy
	}
	return nil
}

func (r *retryResultRepoStub) PruneOldResults(context.Context, int64, int) error { return nil }

func (r *retryResultRepoStub) countCreated() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.created)
}

type retryAccountRepoStub struct {
	AccountRepository
	account *Account
}

func (r retryAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	return r.account, nil
}

func TestScheduledTestRetryResultTargetsOnlyFailedAccount(t *testing.T) {
	accountID, groupID := int64(12), int64(7)
	plan := &ScheduledTestPlan{ID: 3, Name: "all accounts", GroupID: &groupID, TargetMode: "all_accounts", ModelID: "gpt-6-astra"}
	previous := &ScheduledTestResult{ID: 9, PlanID: plan.ID, Status: "failed", AccountID: &accountID, TestName: "Pelican", GroupName: "Group"}
	repo := &retryResultRepoStub{previous: previous}
	svc := NewScheduledTestService(retryPlanRepoStub{plan: plan}, repo)
	called := false
	svc.SetRetryFunc(func(_ context.Context, gotPlan *ScheduledTestPlan, gotAccountID int64) (*ScheduledTestResult, error) {
		called = true
		require.Equal(t, plan, gotPlan)
		require.Equal(t, accountID, gotAccountID)
		return &ScheduledTestResult{ID: 10, PlanID: gotPlan.ID, AccountID: &gotAccountID, Status: "running"}, nil
	})
	result, err := svc.RetryResult(context.Background(), previous.ID)
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "running", result.Status)
	require.Equal(t, int64(10), result.ID)
	require.Equal(t, previous.TestName, result.TestName)
	require.Equal(t, plan.TargetMode, result.TargetMode)
	require.Nil(t, plan.AccountID, "retry must not turn a group plan into a single-account plan")
	require.Equal(t, "failed", previous.Status, "retry must preserve the original failure")
}

func TestScheduledTestRetryResultRejectsNonFailuresAndMissingAccounts(t *testing.T) {
	accountID := int64(12)
	for _, previous := range []*ScheduledTestResult{
		{ID: 1, Status: "success", AccountID: &accountID},
		{ID: 2, Status: "running", AccountID: &accountID},
		{ID: 3, Status: "failed"},
	} {
		svc := NewScheduledTestService(nil, &retryResultRepoStub{previous: previous})
		svc.SetRetryFunc(func(context.Context, *ScheduledTestPlan, int64) (*ScheduledTestResult, error) {
			t.Fatal("invalid result started an upstream retry")
			return nil, nil
		})
		_, err := svc.RetryResult(context.Background(), previous.ID)
		require.Error(t, err)
	}
	svc := NewScheduledTestService(nil, &retryResultRepoStub{})
	_, err := svc.RetryResult(context.Background(), 9)
	require.ErrorIs(t, err, sql.ErrNoRows)
}

func TestScheduledTestRetryCreatesRunningRowAndSurvivesRequestCancellation(t *testing.T) {
	accountID, groupID := int64(12), int64(7)
	plan := &ScheduledTestPlan{ID: 3, GroupID: &groupID, TargetMode: "all_accounts", ModelID: "gpt-6-astra", ReasoningEffort: "high", MaxResults: 50}
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 1)}
	plans := &runnerPlanRepoStub{}
	runner := NewScheduledTestRunnerService(plans, NewScheduledTestService(plans, results), nil,
		retryAccountRepoStub{account: &Account{ID: accountID, GroupIDs: []int64{groupID}, Status: "error", Schedulable: false}}, nil, nil)
	// Keep the background call queued so duplicate protection can be tested
	// deterministically without contacting an upstream.
	runner.workerSem = make(chan struct{}, 1)
	runner.workerSem <- struct{}{}
	require.True(t, runner.beginPlanRun(plan.ID), "simulate other group accounts still executing")
	defer runner.endPlanRun(plan.ID)

	ctx, cancel := context.WithCancel(context.Background())
	pending, err := runner.RetryAccount(ctx, plan, accountID)
	require.NoError(t, err)
	require.Equal(t, "running", pending.Status)
	require.Equal(t, accountID, *pending.AccountID)
	require.Equal(t, "high", pending.ReasoningEffort)
	require.Equal(t, 1, results.countCreated())
	_, err = runner.RetryAccount(ctx, plan, accountID)
	require.ErrorIs(t, err, ErrScheduledTestAccountRunning)
	// A cron/manual full-plan run shares the exact account guard.
	runner.runOneAccount(ctx, plan, accountID, "", "text")
	require.Equal(t, 1, results.countCreated())

	cancel()
	<-runner.workerSem
	select {
	case completed := <-results.updated:
		require.Equal(t, pending.ID, completed.ID)
		require.Equal(t, accountID, *completed.AccountID)
		require.Equal(t, "failed", completed.Status)
		require.Equal(t, "account test service unavailable", completed.ErrorMessage)
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not complete after request cancellation")
	}
	require.Equal(t, "running", pending.Status, "the response must remain an immutable running snapshot")
	require.Equal(t, 0, plans.updated, "individual retries must not move the cron schedule")
	require.Nil(t, plan.AccountID)
}

func TestScheduledTestRetryRejectsAccountRemovedFromGroup(t *testing.T) {
	accountID, groupID := int64(12), int64(7)
	plan := &ScheduledTestPlan{ID: 3, GroupID: &groupID, TargetMode: "all_accounts"}
	results := &retryResultRepoStub{}
	runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, results), nil,
		retryAccountRepoStub{account: &Account{ID: accountID, GroupIDs: []int64{8}}}, nil, nil)
	_, err := runner.RetryAccount(context.Background(), plan, accountID)
	require.ErrorContains(t, err, "no longer assigned")
	require.Equal(t, 0, results.countCreated())
	_, secondErr := runner.RetryAccount(context.Background(), plan, accountID)
	require.False(t, errors.Is(secondErr, ErrScheduledTestAccountRunning), "validation failures must release the account guard")
}
