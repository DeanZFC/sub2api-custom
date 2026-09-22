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
	previous   *ScheduledTestResult
	mu         sync.Mutex
	created    []*ScheduledTestResult
	updated    chan *ScheduledTestResult
	restarted  []*ScheduledTestResult
	restartErr error
}

func (r *retryResultRepoStub) GetByID(context.Context, int64) (*ScheduledTestResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.previous == nil {
		return nil, sql.ErrNoRows
	}
	snapshot := *r.previous
	return &snapshot, nil
}

func (r *retryResultRepoStub) RestartFailed(ctx context.Context, result *ScheduledTestResult) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.restartErr != nil {
		return r.restartErr
	}
	if r.previous == nil || r.previous.ID != result.ID || r.previous.Status != "failed" {
		return ErrScheduledTestResultNotFailed
	}
	snapshot := *result
	r.previous = &snapshot
	r.restarted = append(r.restarted, &snapshot)
	return nil
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
	r.mu.Lock()
	r.previous = &copy
	r.mu.Unlock()
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
	svc.SetRetryFunc(func(_ context.Context, gotPlan *ScheduledTestPlan, gotPrevious *ScheduledTestResult) (*ScheduledTestResult, error) {
		called = true
		require.Equal(t, plan, gotPlan)
		require.Equal(t, previous, gotPrevious)
		return &ScheduledTestResult{ID: gotPrevious.ID, PlanID: gotPlan.ID, AccountID: gotPrevious.AccountID, Status: "running"}, nil
	})
	result, err := svc.RetryResult(context.Background(), previous.ID)
	require.NoError(t, err)
	require.True(t, called)
	require.Equal(t, "running", result.Status)
	require.Equal(t, previous.ID, result.ID)
	require.Equal(t, previous.TestName, result.TestName)
	require.Equal(t, plan.TargetMode, result.TargetMode)
	require.Nil(t, plan.AccountID, "retry must not turn a group plan into a single-account plan")
	require.Equal(t, "failed", previous.Status, "the request snapshot must not be mutated")
}

func TestScheduledTestRetryResultRejectsNonFailuresAndMissingAccounts(t *testing.T) {
	accountID := int64(12)
	for _, previous := range []*ScheduledTestResult{
		{ID: 1, Status: "success", AccountID: &accountID},
		{ID: 2, Status: "running", AccountID: &accountID},
		{ID: 3, Status: "failed"},
	} {
		svc := NewScheduledTestService(nil, &retryResultRepoStub{previous: previous})
		svc.SetRetryFunc(func(context.Context, *ScheduledTestPlan, *ScheduledTestResult) (*ScheduledTestResult, error) {
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

func TestScheduledTestRetryReusesRunningRowAndSurvivesRequestCancellation(t *testing.T) {
	accountID, groupID := int64(12), int64(7)
	plan := &ScheduledTestPlan{ID: 3, GroupID: &groupID, TargetMode: "all_accounts", ModelID: "gpt-6-astra", ReasoningEffort: "high", MaxResults: 50}
	oldNumber := 29.0
	previous := &ScheduledTestResult{ID: 9, PlanID: plan.ID, AccountID: &accountID, Status: "failed", OutputKind: "number", ResponseText: "old output", OutputHTML: "<svg />", OutputNumeric: &oldNumber, ErrorMessage: "old failure", LatencyMs: 1234, CreatedAt: time.Now().Add(-time.Hour), StartedAt: time.Now().Add(-time.Minute)}
	results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 1)}
	plans := &runnerPlanRepoStub{}
	runner := NewScheduledTestRunnerService(plans, NewScheduledTestService(plans, results), nil,
		retryAccountRepoStub{account: &Account{ID: accountID, GroupIDs: []int64{groupID}, Status: "error", Schedulable: false}}, nil, nil)
	runner.automaticRetryBaseDelay = time.Millisecond
	// Keep the background call queued so duplicate protection can be tested
	// deterministically without contacting an upstream.
	runner.workerSem = make(chan struct{}, 1)
	runner.workerSem <- struct{}{}
	require.True(t, runner.beginPlanRun(plan.ID), "simulate other group accounts still executing")
	defer runner.endPlanRun(plan.ID)

	ctx, cancel := context.WithCancel(context.Background())
	pending, err := runner.RetryAccount(ctx, plan, previous)
	require.NoError(t, err)
	require.Equal(t, "running", pending.Status)
	require.Equal(t, previous.ID, pending.ID)
	require.Equal(t, previous.CreatedAt, pending.CreatedAt)
	require.True(t, pending.StartedAt.After(previous.StartedAt))
	require.Empty(t, pending.ResponseText)
	require.Empty(t, pending.OutputHTML)
	require.Nil(t, pending.OutputNumeric)
	require.Empty(t, pending.ErrorMessage)
	require.Zero(t, pending.LatencyMs)
	require.Len(t, results.restarted, 1)
	require.Equal(t, accountID, *pending.AccountID)
	require.Equal(t, "high", pending.ReasoningEffort)
	require.Zero(t, results.countCreated())
	_, err = runner.RetryAccount(ctx, plan, previous)
	require.ErrorIs(t, err, ErrScheduledTestAccountRunning)
	// A cron/manual full-plan run shares the exact account guard.
	runner.runOneAccount(ctx, plan, accountID, "", "text")
	require.Zero(t, results.countCreated())

	cancel()
	<-runner.workerSem
	select {
	case completed := <-results.updated:
		require.Equal(t, pending.ID, completed.ID)
		require.Equal(t, previous.CreatedAt, completed.CreatedAt)
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
	_, err := runner.RetryAccount(context.Background(), plan, &ScheduledTestResult{ID: 9, PlanID: plan.ID, Status: "failed", AccountID: &accountID})
	require.ErrorContains(t, err, "no longer assigned")
	require.Equal(t, 0, results.countCreated())
	_, secondErr := runner.RetryAccount(context.Background(), plan, &ScheduledTestResult{ID: 9, PlanID: plan.ID, Status: "failed", AccountID: &accountID})
	require.False(t, errors.Is(secondErr, ErrScheduledTestAccountRunning), "validation failures must release the account guard")
}

func TestScheduledTestRetryResetFailureDoesNotStartExecution(t *testing.T) {
	for _, cause := range []error{ErrScheduledTestResultNotFailed, errors.New("database unavailable")} {
		t.Run(cause.Error(), func(t *testing.T) {
			accountID := int64(12)
			previous := &ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, Status: "failed", ErrorMessage: "original failure"}
			results := &retryResultRepoStub{previous: previous, restartErr: cause, updated: make(chan *ScheduledTestResult, 1)}
			runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, results), nil,
				retryAccountRepoStub{account: &Account{ID: accountID}}, nil, nil)
			pending, err := runner.RetryAccount(context.Background(), &ScheduledTestPlan{ID: 3}, previous)
			require.ErrorIs(t, err, cause)
			require.Nil(t, pending)
			require.Zero(t, results.countCreated())
			require.Empty(t, results.restarted)
			require.Empty(t, results.updated)
			require.Equal(t, "original failure", results.previous.ErrorMessage)
			require.True(t, runner.beginAccountRun(3, accountID, nil), "failed reset must release its account guard")
			runner.endAccountRun(3, accountID, nil)
		})
	}
}

func TestScheduledTestRetrySuccessUpdatesOriginalResult(t *testing.T) {
	accountID := int64(12)
	previous := &ScheduledTestResult{ID: 9, PlanID: 3, AccountID: &accountID, Status: "failed", ResponseText: "old output", ErrorMessage: "original failure", CreatedAt: time.Now().Add(-time.Hour)}
	results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 1)}
	accountRepo := retryAccountRepoStub{account: &Account{ID: accountID, Extra: map[string]any{"synthetic_ui_test": true}}}
	svc := NewScheduledTestService(retryPlanRepoStub{plan: &ScheduledTestPlan{ID: 3, MaxResults: 50}}, results)
	runner := NewScheduledTestRunnerService(nil, svc, &AccountTestService{accountRepo: accountRepo}, accountRepo, nil, nil)
	svc.SetRetryFunc(runner.RetryAccount)
	pending, err := svc.RetryResult(context.Background(), previous.ID)
	require.NoError(t, err)
	require.Equal(t, previous.ID, pending.ID)
	select {
	case completed := <-results.updated:
		require.Equal(t, previous.ID, completed.ID)
		require.Equal(t, previous.CreatedAt, completed.CreatedAt)
		require.Equal(t, "success", completed.Status)
		require.Empty(t, completed.ErrorMessage)
		require.NotEmpty(t, completed.ResponseText)
		require.NotEqual(t, "old output", completed.ResponseText)
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not complete")
	}
	require.Zero(t, results.countCreated())
	_, err = svc.RetryResult(context.Background(), previous.ID)
	require.ErrorContains(t, err, "only failed")
}
