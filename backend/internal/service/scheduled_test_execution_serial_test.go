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
	for _, kind := range []string{"text"} {
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
			repo := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 4)}
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
