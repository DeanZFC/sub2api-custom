package service

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func automaticRetryTestPlan() *ScheduledTestPlan {
	plan := scheduledTestExecutionPlan()
	plan.TestDefinitionIDs = []int64{1}
	return plan
}

func automaticRetryTestContext(parent context.Context) context.Context {
	return context.WithValue(parent, scheduledTestAutomaticRunKey{}, true)
}

func automaticRetryTestRunner(results ScheduledTestResultRepository, accounts AccountRepository) *ScheduledTestRunnerService {
	runner := newScheduledTestExecutionRunner(results, accounts)
	runner.automaticRetryBaseDelay = time.Millisecond
	runner.executionTimeout = time.Second
	return runner
}

func automaticRetryCompleted(t *testing.T, results *retryResultRepoStub) *ScheduledTestResult {
	t.Helper()
	select {
	case result := <-results.updated:
		return result
	case <-time.After(2 * time.Second):
		t.Fatal("test did not persist its final result")
		return nil
	}
}

func requireAutomaticRetrySingleRow(t *testing.T, results *retryResultRepoStub, completed *ScheduledTestResult) {
	t.Helper()
	require.Equal(t, 1, results.countCreated(), "automatic attempts must share one visible execution")
	require.Equal(t, "running", results.created[0].Status)
	require.Equal(t, results.created[0].ID, completed.ID)
	require.Empty(t, results.updated, "only the final attempt should complete the execution row")
}

func TestScheduledTestAutomaticRetryStopsAfterSuccessAndPersistsOnce(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		if calls.Add(1) <= 2 {
			// The real AccountTestService turns this into Status=failed while
			// returning a nil Go error to the runner.
			return nil, errors.New("temporary upstream account lookup failure")
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.runOnePlan(automaticRetryTestContext(context.Background()), automaticRetryTestPlan())
	completed := automaticRetryCompleted(t, results)
	require.Equal(t, int32(3), calls.Load(), "failed status with nil error must retry; success must stop retries")
	require.Equal(t, "success", completed.Status)
	require.Empty(t, completed.ErrorMessage)
	require.NotEmpty(t, completed.ResponseText)
	requireAutomaticRetrySingleRow(t, results, completed)
}

func TestScheduledTestAutomaticRetryExhaustsExactlyThreeRetries(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
		calls.Add(1)
		return nil, errors.New("persistent upstream failure")
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.runOnePlan(automaticRetryTestContext(context.Background()), automaticRetryTestPlan())
	completed := automaticRetryCompleted(t, results)
	require.Equal(t, int32(4), calls.Load(), "one initial execution plus three retries")
	require.Equal(t, "failed", completed.Status)
	require.NotEmpty(t, completed.ErrorMessage)
	requireAutomaticRetrySingleRow(t, results, completed)
}

func TestScheduledTestAutomaticRetryIncludesOutputContractFailures(t *testing.T) {
	for _, kind := range []string{"html", "number"} {
		t.Run(kind, func(t *testing.T) {
			results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
			var calls atomic.Int32
			accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
				calls.Add(1)
				// This probe succeeds with ordinary text, which does not satisfy
				// the requested HTML or numeric result contract.
				return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
			}}
			runner := automaticRetryTestRunner(results, accounts)
			defer runner.Stop()
			runner.scheduledSvc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, Prompt: "produce structured output", OutputKind: kind},
			}})
			runner.runOnePlan(automaticRetryTestContext(context.Background()), automaticRetryTestPlan())
			completed := automaticRetryCompleted(t, results)
			require.Equal(t, int32(4), calls.Load(), "output parsing must happen inside each automatic attempt")
			require.Equal(t, "failed", completed.Status)
			require.Contains(t, completed.ErrorMessage, "expected")
			requireAutomaticRetrySingleRow(t, results, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryContinuesOtherDefinitionsAfterExhaustion(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 8)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		calls.Add(1)
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.scheduledSvc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "generate HTML", OutputKind: "html"},
		2: {ID: 2, Enabled: true, Prompt: "generate text", OutputKind: "text"},
	}})
	plan := automaticRetryTestPlan()
	plan.TestDefinitionIDs = []int64{1, 2}
	runner.runOnePlan(automaticRetryTestContext(context.Background()), plan)
	first := automaticRetryCompleted(t, results)
	second := automaticRetryCompleted(t, results)
	require.Equal(t, int32(5), calls.Load(), "the invalid HTML consumes four attempts, then the text check runs once")
	require.Equal(t, int64(1), *first.TestDefinitionID)
	require.Equal(t, "failed", first.Status)
	require.Contains(t, first.ErrorMessage, "expected HTML")
	require.Equal(t, int64(2), *second.TestDefinitionID)
	require.Equal(t, "success", second.Status)
	require.Equal(t, 2, results.countCreated(), "each configured type retains its own single execution record")
	require.Equal(t, results.created[0].ID, first.ID)
	require.Equal(t, results.created[1].ID, second.ID)
	require.Empty(t, results.updated)
	require.Equal(t, 1, runner.planRepo.(*runnerPlanRepoStub).updated, "one multi-type rule advances its schedule once")
}

func TestScheduledTestAutomaticRetryGetsFreshTimeoutAfterTimedOutAttempt(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	var firstDeadline time.Time
	var secondBudget time.Duration
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		deadline, bounded := ctx.Deadline()
		if !bounded {
			return nil, errors.New("attempt has no timeout")
		}
		if calls.Add(1) == 1 {
			firstDeadline = deadline
			<-ctx.Done()
			return nil, ctx.Err()
		}
		secondBudget = time.Until(deadline)
		if !deadline.After(firstDeadline) || ctx.Err() != nil || secondBudget <= 0 {
			return nil, fmt.Errorf("retry inherited an expired execution timeout")
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := automaticRetryTestRunner(results, accounts)
	runner.executionTimeout = 40 * time.Millisecond
	defer runner.Stop()
	runner.runOnePlan(automaticRetryTestContext(context.Background()), automaticRetryTestPlan())
	completed := automaticRetryCompleted(t, results)
	require.Equal(t, int32(2), calls.Load())
	require.Positive(t, secondBudget)
	require.Equal(t, "success", completed.Status)
	requireAutomaticRetrySingleRow(t, results, completed)
}

func TestScheduledTestAutomaticRetryBackoffReleasesWorkerAndCanBeInterrupted(t *testing.T) {
	for _, interrupt := range []string{"cancel", "stop"} {
		t.Run(interrupt, func(t *testing.T) {
			results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
			entered := make(chan struct{}, 5)
			var calls atomic.Int32
			accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
				calls.Add(1)
				entered <- struct{}{}
				return nil, errors.New("retryable failure")
			}}
			runner := automaticRetryTestRunner(results, accounts)
			runner.automaticRetryBaseDelay = time.Hour
			runner.workerSem = make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				runner.runOnePlan(automaticRetryTestContext(ctx), automaticRetryTestPlan())
			}()
			t.Cleanup(func() {
				cancel()
				runner.Stop()
			})
			select {
			case <-entered:
			case <-time.After(time.Second):
				t.Fatal("initial attempt did not start")
			}
			// A failed test must not monopolize scarce upstream workers while
			// waiting to retry. Acquiring the only worker proves it is free.
			workerCtx, cancelWorker := context.WithTimeout(context.Background(), time.Second)
			acquired := runner.acquireWorker(workerCtx)
			cancelWorker()
			require.True(t, acquired, "retry backoff retained its model worker")
			runner.releaseWorker()
			if interrupt == "cancel" {
				cancel()
			} else {
				stopped := make(chan struct{})
				go func() { runner.Stop(); close(stopped) }()
				select {
				case <-stopped:
				case <-time.After(time.Second):
					t.Fatal("shutdown did not interrupt the retry delay")
				}
			}
			select {
			case <-done:
			case <-time.After(time.Second):
				t.Fatal("cancellation did not interrupt the retry delay")
			}
			require.Equal(t, int32(1), calls.Load(), "interruption must not start another upstream attempt")
			completed := automaticRetryCompleted(t, results)
			require.Equal(t, "failed", completed.Status)
			requireAutomaticRetrySingleRow(t, results, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryDoesNotApplyToManualRunOrSingleResultRetry(t *testing.T) {
	t.Run("RunPlanNow", func(t *testing.T) {
		results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
		var calls atomic.Int32
		accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
			calls.Add(1)
			return nil, errors.New("manual test failure")
		}}
		runner := automaticRetryTestRunner(results, accounts)
		defer runner.Stop()
		runner.RunPlanNow(context.Background(), automaticRetryTestPlan())
		completed := automaticRetryCompleted(t, results)
		require.Equal(t, int32(1), calls.Load())
		require.Equal(t, "failed", completed.Status)
		requireAutomaticRetrySingleRow(t, results, completed)
	})

	t.Run("RetryAccount", func(t *testing.T) {
		plan := automaticRetryTestPlan()
		previous := &ScheduledTestResult{
			ID: 90, PlanID: plan.ID, TestDefinitionID: scheduledTestPtrInt64(1), TargetMode: "account",
			AccountID: plan.AccountID, ModelID: plan.ModelID, Status: "failed", CreatedAt: time.Now().Add(-time.Hour),
		}
		results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 5)}
		var calls atomic.Int32
		accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
			if calls.Add(1) == 1 {
				return &Account{ID: id}, nil // Admin request validates the account.
			}
			return nil, errors.New("single-result retry failure")
		}}
		runner := automaticRetryTestRunner(results, accounts)
		defer runner.Stop()
		pending, err := runner.RetryAccount(context.Background(), plan, previous)
		require.NoError(t, err)
		completed := automaticRetryCompleted(t, results)
		runner.activeRuns.Wait()
		require.Equal(t, int32(2), calls.Load(), "one validation and one actual attempt")
		require.Equal(t, previous.ID, pending.ID)
		require.Equal(t, previous.ID, completed.ID)
		require.Equal(t, "failed", completed.Status)
		require.Zero(t, results.countCreated())
		require.Empty(t, results.updated)
	})
}

func TestScheduledTestAutomaticRetryCronDiscoveryEnablesRetries(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
		calls.Add(1)
		return nil, errors.New("scheduled test failure")
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.planRepo = &scheduledTestDiscoveryPlanRepo{due: []*ScheduledTestPlan{automaticRetryTestPlan()}}
	// Use the real cron discovery entrypoint, with no test-only context marker.
	ctx, cancel := context.WithCancel(context.Background())
	runner.runDuePlans(ctx)
	cancel()
	completed := automaticRetryCompleted(t, results)
	runner.activeRuns.Wait()
	require.Equal(t, int32(4), calls.Load(), "admitted cron work must receive automatic retries and outlive discovery")
	require.Equal(t, "failed", completed.Status)
	requireAutomaticRetrySingleRow(t, results, completed)
}
