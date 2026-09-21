package service

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type scheduledTestExecutionAccountRepo struct {
	AccountRepository
	get      func(context.Context, int64) (*Account, error)
	accounts []Account
}

func (r scheduledTestExecutionAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	return r.get(ctx, id)
}

func (r scheduledTestExecutionAccountRepo) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

type scheduledTestDiscoveryPlanRepo struct {
	ScheduledTestPlanRepository
	due []*ScheduledTestPlan
}

func (r *scheduledTestDiscoveryPlanRepo) ListDue(context.Context, time.Time) ([]*ScheduledTestPlan, error) {
	return r.due, nil
}

func (*scheduledTestDiscoveryPlanRepo) UpdateAfterRun(context.Context, int64, time.Time, time.Time) error {
	return nil
}

func scheduledTestExecutionPlan() *ScheduledTestPlan {
	return &ScheduledTestPlan{
		ID: 7, AccountID: scheduledTestPtrInt64(12), TargetMode: "account",
		TestDefinitionIDs: []int64{1, 2}, ModelID: "model", CronExpression: "* * * * *", MaxResults: 10,
	}
}

func newScheduledTestExecutionRunner(results ScheduledTestResultRepository, accounts AccountRepository) *ScheduledTestRunnerService {
	plans := &runnerPlanRepoStub{}
	svc := NewScheduledTestService(plans, results)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "first", OutputKind: "text"},
		2: {ID: 2, Enabled: true, Prompt: "second", OutputKind: "text"},
	}})
	runner := NewScheduledTestRunnerService(plans, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	runner.executionTimeout = 40 * time.Millisecond
	return runner
}

func TestScheduledTestEachDefinitionGetsFreshExecutionTimeout(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 2)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		if _, bounded := ctx.Deadline(); !bounded {
			return nil, fmt.Errorf("account execution has no deadline")
		}
		if calls.Add(1) == 1 {
			<-ctx.Done() // The first type consumes its entire execution budget.
			return nil, ctx.Err()
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newScheduledTestExecutionRunner(results, accounts)
	defer runner.Stop()
	runner.RunPlanNow(context.Background(), scheduledTestExecutionPlan())
	first, second := <-results.updated, <-results.updated
	require.Equal(t, int64(1), *first.TestDefinitionID)
	require.Equal(t, "failed", first.Status)
	require.Equal(t, int64(2), *second.TestDefinitionID)
	require.Equal(t, "success", second.Status, "the first type's timeout must not cancel the second type")
	require.Equal(t, int32(2), calls.Load())
}

func TestScheduledTestWorkerQueueDoesNotConsumeExecutionTimeout(t *testing.T) {
	results := &multiDefinitionObservedResultRepo{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 1)},
		started:             make(chan *ScheduledTestResult, 1),
	}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		calls.Add(1)
		deadline, bounded := ctx.Deadline()
		if !bounded || time.Until(deadline) <= 0 || ctx.Err() != nil {
			return nil, fmt.Errorf("execution did not receive a fresh timeout after queueing")
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newScheduledTestExecutionRunner(results, accounts)
	defer runner.Stop()
	runner.workerSem = make(chan struct{}, 1)
	runner.workerSem <- struct{}{}
	plan := scheduledTestExecutionPlan()
	plan.TestDefinitionIDs = []int64{1}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.RunPlanNow(context.Background(), plan)
	}()
	select {
	case <-results.started:
	case <-time.After(time.Second):
		t.Fatal("test did not enter the worker queue")
	}
	time.Sleep(3 * runner.executionTimeout)
	require.Zero(t, calls.Load(), "queued work must not contact the account")
	<-runner.workerSem
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("test did not finish after a worker became available")
	}
	require.Equal(t, "success", (<-results.updated).Status)
}

func TestScheduledTestShutdownCancelsManualRunsAndRetries(t *testing.T) {
	for _, retry := range []bool{false, true} {
		for _, queued := range []bool{false, true} {
			t.Run(fmt.Sprintf("retry=%v/queued=%v", retry, queued), func(t *testing.T) {
				accountID := int64(12)
				previous := &ScheduledTestResult{ID: 9, PlanID: 7, AccountID: &accountID, TestDefinitionID: scheduledTestPtrInt64(1), Status: "failed"}
				results := &multiDefinitionObservedResultRepo{
					retryResultRepoStub: &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 1)},
					started:             make(chan *ScheduledTestResult, 1),
				}
				entered := make(chan struct{}, 1)
				var accountReads atomic.Int32
				accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
					// Retry validates the account using the admin request before
					// its worker starts; let that first lookup complete.
					if accountReads.Add(1) == 1 && retry {
						return &Account{ID: id}, nil
					}
					entered <- struct{}{}
					<-ctx.Done()
					return nil, ctx.Err()
				}}
				runner := newScheduledTestExecutionRunner(results, accounts)
				runner.executionTimeout = time.Minute
				defer runner.Stop()
				if queued {
					runner.workerSem = make(chan struct{}, 1)
					runner.workerSem <- struct{}{}
				}
				plan := scheduledTestExecutionPlan()
				plan.TestDefinitionIDs = []int64{1}
				if retry {
					pending, err := runner.RetryAccount(context.Background(), plan, previous)
					require.NoError(t, err)
					require.Equal(t, previous.ID, pending.ID)
				} else {
					go runner.RunPlanNow(context.Background(), plan)
					select {
					case <-results.started:
					case <-time.After(time.Second):
						t.Fatal("manual run did not start")
					}
				}
				if !queued {
					select {
					case <-entered:
					case <-time.After(time.Second):
						t.Fatal("account test did not start")
					}
				}
				runner.Stop()
				select {
				case completed := <-results.updated:
					require.Equal(t, "failed", completed.Status)
					if retry {
						require.Equal(t, previous.ID, completed.ID)
						require.Zero(t, results.countCreated(), "retry must reuse the existing result")
					}
				default:
					t.Fatal("Stop returned before cancellation was persisted")
				}
				_, _, err := runner.beginRunContext(context.Background())
				require.ErrorIs(t, err, context.Canceled, "shutdown must reject new background runs")
			})
		}
	}
}

func TestScheduledTestRunNowDoesNotSetSharedDeadline(t *testing.T) {
	plan := scheduledTestExecutionPlan()
	svc := NewScheduledTestService(retryPlanRepoStub{plan: plan}, nil)
	observed := make(chan context.Context, 1)
	svc.SetRunFunc(func(ctx context.Context, _ *ScheduledTestPlan) { observed <- ctx })
	request, cancel := context.WithCancel(context.Background())
	require.NoError(t, svc.RunNow(request, plan.ID))
	cancel()
	select {
	case ctx := <-observed:
		_, bounded := ctx.Deadline()
		require.False(t, bounded, "a rule deadline would consume later types' execution budgets")
	case <-time.After(time.Second):
		t.Fatal("manual run did not dispatch")
	}
}

func TestScheduledTestParentCancellationStopsQueuedAccounts(t *testing.T) {
	results := &multiDefinitionObservedResultRepo{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 20)},
		started:             make(chan *ScheduledTestResult, 20),
	}
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, _ int64) (*Account, error) {
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	for id := int64(1); id <= 20; id++ {
		accounts.accounts = append(accounts.accounts, Account{ID: id})
	}
	runner := newScheduledTestExecutionRunner(results, accounts)
	runner.executionTimeout = time.Minute
	runner.workerSem = make(chan struct{}, 1)
	defer runner.Stop()
	plan := scheduledTestExecutionPlan()
	plan.AccountID = nil
	plan.GroupID = scheduledTestPtrInt64(8)
	plan.TargetMode = "all_accounts"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.RunPlanNow(ctx, plan)
	}()
	for range scheduledTestDefaultMaxWorkers {
		select {
		case <-results.started:
		case <-time.After(time.Second):
			t.Fatal("first account batch did not queue")
		}
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("parent cancellation did not stop execution and queueing")
	}
	require.Equal(t, scheduledTestDefaultMaxWorkers, results.countCreated(), "cancellation must not create rows for undispatched accounts or later types")
	for range scheduledTestDefaultMaxWorkers {
		require.Equal(t, "failed", (<-results.updated).Status, "already visible rows must leave running")
	}
}

func TestScheduledTestDiscoveryContinuesWhileEarlierRuleRuns(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 2)}
	type execution struct {
		accountID int64
		ctx       context.Context
	}
	entered := make(chan execution, 3)
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		calls.Add(1)
		entered <- execution{accountID: id, ctx: ctx}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	runner := newScheduledTestExecutionRunner(results, accounts)
	runner.executionTimeout = time.Minute
	defer runner.Stop()
	first := scheduledTestExecutionPlan()
	first.TestDefinitionIDs = []int64{1}
	second := *first
	second.ID = 8
	second.AccountID = scheduledTestPtrInt64(13)
	plans := &scheduledTestDiscoveryPlanRepo{due: []*ScheduledTestPlan{first}}
	runner.planRepo = plans
	scan := func() {
		t.Helper()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		done := make(chan struct{})
		go func() {
			defer close(done)
			runner.runDuePlans(ctx)
		}()
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("discovery waited for a slow rule to complete")
		}
	}
	nextExecution := func() execution {
		t.Helper()
		select {
		case execution := <-entered:
			return execution
		case <-time.After(time.Second):
			t.Fatal("newly due rule was not started")
			return execution{}
		}
	}
	scan()
	firstExecution := nextExecution()
	require.Equal(t, int64(12), firstExecution.accountID)
	require.NoError(t, firstExecution.ctx.Err(), "finishing discovery must not cancel an admitted rule")
	plans.due = []*ScheduledTestPlan{first, &second}
	scan()
	secondExecution := nextExecution()
	require.Equal(t, int64(13), secondExecution.accountID)
	require.NoError(t, secondExecution.ctx.Err())
	require.Equal(t, int32(2), calls.Load(), "a repeated tick must not duplicate the still-running rule")
	runner.Stop()
	require.ErrorIs(t, firstExecution.ctx.Err(), context.Canceled)
	require.ErrorIs(t, secondExecution.ctx.Err(), context.Canceled)
	require.Equal(t, 2, results.countCreated())
}
