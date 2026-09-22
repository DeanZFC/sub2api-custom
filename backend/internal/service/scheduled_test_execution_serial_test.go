package service

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestAccountExecutionSerializesOtherDefinitions(t *testing.T) {
	for _, kind := range []string{"text", "statistics"} {
		t.Run(kind, func(t *testing.T) {
			const accountID = int64(42)
			entered := make(chan int64, 4)
			releaseFirst := make(chan struct{})
			var releaseOnce sync.Once
			defer releaseOnce.Do(func() { close(releaseFirst) })
			var calls atomic.Int32
			accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
				entered <- id
				if id == accountID && calls.Add(1) == 1 {
					select {
					case <-releaseFirst:
					case <-ctx.Done():
						return nil, ctx.Err()
					}
				}
				return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
			}}
			repo := &statisticsResultRepoStub{
				retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 4)},
				collect: func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
					entered <- *filter.AccountID
					return &ScheduledTestStatistics{}, nil
				},
			}
			runner := newScheduledTestExecutionRunner(repo, accounts)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var wg sync.WaitGroup
			run := func(id, definition int64, outputKind string) {
				wg.Add(1)
				go func() {
					defer wg.Done()
					plan := &ScheduledTestPlan{ID: 7, TestDefinitionID: &definition, ModelID: "model", MaxResults: 50}
					runner.runOneAccount(ctx, plan, id, "test", outputKind)
				}()
			}
			run(accountID, 1, "text")
			select {
			case id := <-entered:
				require.Equal(t, accountID, id)
			case <-ctx.Done():
				t.Fatal("first account execution did not start")
			}
			run(accountID, 2, kind)
			run(43, 1, "text")
			select {
			case id := <-entered:
				require.EqualValues(t, 43, id, "another definition must wait, while another account may proceed")
			case <-ctx.Done():
				t.Fatal("independent account did not proceed")
			}
			require.Eventually(t, func() bool { return repo.countCreated() == 3 }, time.Second, time.Millisecond)
			select {
			case id := <-entered:
				t.Fatalf("account %d overlapped its first execution", id)
			case <-time.After(25 * time.Millisecond):
			}
			releaseOnce.Do(func() { close(releaseFirst) })
			wg.Wait()
			require.NoError(t, ctx.Err())
			require.Len(t, repo.updated, 3, "queued definitions must complete, not be skipped")
			for range 3 {
				require.Equal(t, "success", (<-repo.updated).Status)
			}
		})
	}
}

type statisticsRunEligibilityRepo struct {
	*statisticsResultRepoStub
	current atomic.Bool
	checked chan struct{}
}

func (r *statisticsRunEligibilityRepo) IsPlanRunAccountEligible(context.Context, int64, string, int64) (bool, error) {
	eligible := r.current.Load()
	r.checked <- struct{}{}
	return eligible, nil
}

func TestScheduledTestStatisticsQueuedRunInvalidatedBeforeCollection(t *testing.T) {
	repo := &statisticsRunEligibilityRepo{
		statisticsResultRepoStub: &statisticsResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 3)}},
		checked:                  make(chan struct{}, 5),
	}
	repo.current.Store(true)
	runner := NewScheduledTestRunnerService(nil, statisticsTestService(repo), nil, nil, nil, nil)
	runner.automaticRetryBaseDelay = time.Millisecond
	runner.statisticsSem = make(chan struct{}, 1)
	runner.statisticsSem <- struct{}{}
	plan := statisticsTestPlan()
	accountID := int64(42)
	started := time.Now()
	pending := &ScheduledTestResult{ID: 7, PlanID: plan.ID, RunID: "before-edit", TestDefinitionID: plan.TestDefinitionID, AccountID: &accountID, Status: "pending", StartedAt: started}
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.runExecutionTarget(context.Background(), &scheduledTestExecutionTarget{plan: plan, kind: "statistics", accountID: &accountID, pending: pending}, started)
	}()
	select {
	case <-repo.checked:
	case <-time.After(time.Second):
		t.Fatal("initial run validation did not finish")
	}
	repo.current.Store(false)
	<-runner.statisticsSem
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("invalidated queued statistics did not finish")
	}
	require.Empty(t, repo.filters, "a changed strategy must invalidate statistics that waited for a worker")
	require.Equal(t, "running", (<-repo.updated).Status)
	completed := <-repo.updated
	require.Equal(t, "failed", completed.Status)
	require.Equal(t, "before-edit", completed.RunID)
	require.Contains(t, completed.ErrorMessage, "no longer enabled")
}

func TestScheduledTestInvalidatedRunDoesNotAutomaticallyRecoverAccount(t *testing.T) {
	for _, invalidated := range []bool{false, true} {
		t.Run(map[bool]string{false: "current", true: "edited while running"}[invalidated], func(t *testing.T) {
			accountID := int64(42)
			repo := &snapshotResultRepoStub{
				retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 1)},
				latestRun:           "original", frozenMembers: map[int64]bool{accountID: true},
			}
			account := &Account{ID: accountID, Status: "active", Schedulable: true, Extra: map[string]any{"synthetic_ui_test": true}}
			accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
				if invalidated {
					repo.latestRun = ""
				}
				return account, nil
			}}
			runner := newSnapshotRunner(repo, accounts)
			recoveries := 0
			runner.rateLimitSvc = &RateLimitService{accountRepo: scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
				recoveries++
				return account, nil
			}}}
			plan := scheduledTestExecutionPlan()
			plan.Protection.Enabled, plan.AutoRecover = false, true
			pending := &ScheduledTestResult{ID: 10, PlanID: plan.ID, AccountID: &accountID, RunID: "original", StartedAt: time.Now()}
			runner.runAccountWithResult(context.Background(), plan, accountID, "test", "text", pending)
			require.Equal(t, "success", (<-repo.updated).Status)
			if invalidated {
				require.Zero(t, recoveries, "old successful results cannot mutate scheduling after a strategy edit")
			} else {
				require.Equal(t, 1, recoveries)
			}
		})
	}
}
