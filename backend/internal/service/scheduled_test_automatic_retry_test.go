package service

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
)

type scheduledTestRetryStateAccountRepo struct {
	AccountRepository
	mu         sync.Mutex
	account    *Account
	errorCalls []string
}

func (r *scheduledTestRetryStateAccountRepo) GetByID(ctx context.Context, id int64) (*Account, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account == nil || r.account.ID != id {
		return nil, ErrAccountNotFound
	}
	account := *r.account
	return &account, nil
}

func (r *scheduledTestRetryStateAccountRepo) SetError(_ context.Context, id int64, message string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.account == nil || r.account.ID != id {
		return ErrAccountNotFound
	}
	r.errorCalls = append(r.errorCalls, message)
	r.account.Status = StatusError
	return nil
}

func (*scheduledTestRetryStateAccountRepo) UpdateExtra(context.Context, int64, map[string]any) error {
	return nil
}

func (r *scheduledTestRetryStateAccountRepo) stopScheduling() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.account.Schedulable = false
}

type scheduledTestRetryStateResultRepo struct {
	*retryResultRepoStub
	ScheduledTestProtectionRepository
	accounts *scheduledTestRetryStateAccountRepo
}

func (r *scheduledTestRetryStateResultRepo) ListDetectionAccountIDs(ctx context.Context, _ *int64, id *int64) ([]int64, error) {
	account, err := r.accounts.GetByID(ctx, *id)
	if err != nil {
		return nil, err
	}
	if account.Status != StatusActive || !account.Schedulable {
		return nil, nil
	}
	return []int64{account.ID}, nil
}

func automaticRetryTestPlan() *ScheduledTestPlan {
	plan := scheduledTestExecutionPlan()
	plan.TestDefinitionIDs = []int64{1}
	return plan
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

func TestScheduledTestAutomaticRetrySuccessPersistsOnce(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		calls.Add(1)
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.runOnePlan(context.Background(), automaticRetryTestPlan())
	completed := automaticRetryCompleted(t, results)
	require.Equal(t, int32(1), calls.Load(), "success must stop retries")
	require.Equal(t, "success", completed.Status)
	require.Empty(t, completed.ErrorMessage)
	require.NotEmpty(t, completed.ResponseText)
	requireAutomaticRetrySingleRow(t, results, completed)
}

func TestScheduledTestAutomaticRetryRetriesAccountLookupFailure(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
		calls.Add(1)
		return nil, errors.New("persistent upstream failure")
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.runOnePlan(context.Background(), automaticRetryTestPlan())
	completed := automaticRetryCompleted(t, results)
	require.Equal(t, int32(4), calls.Load(), "account lookup errors receive three automatic retries")
	require.Equal(t, "failed", completed.Status)
	require.NotEmpty(t, completed.ErrorMessage)
	requireAutomaticRetrySingleRow(t, results, completed)
}

func TestScheduledTestAutomaticRetryDefersAccountErrorUntilFinalAttempt(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			plan := automaticRetryTestPlan()
			accounts := &scheduledTestRetryStateAccountRepo{account: &Account{
				ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}}
			results := &scheduledTestRetryStateResultRepo{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}, accounts: accounts}
			final := costJSONResponse(401, `{"error":{"message":"invalid key again"}}`)
			if recover {
				final = costJSONResponse(200, `{"model":"model","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`)
			}
			upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
				costJSONResponse(401, `{"error":{"message":"invalid key"}}`),
				costJSONResponse(401, `{"error":{"message":"invalid key"}}`),
				costJSONResponse(401, `{"error":{"message":"invalid key"}}`), final,
			}, beforeReturn: func(_ *http.Request, _ int) {
				account, err := accounts.GetByID(context.Background(), *plan.AccountID)
				require.NoError(t, err)
				require.Equal(t, StatusActive, account.Status, "earlier failures must not make the remaining retries ineligible")
			}}
			runner := automaticRetryTestRunner(results, accounts)
			defer runner.Stop()
			runner.accountTestSvc.httpUpstream = upstream
			runner.accountTestSvc.cfg = &config.Config{}
			runner.runOnePlan(context.Background(), plan)
			completed := automaticRetryCompleted(t, results.retryResultRepoStub)
			require.Equal(t, 4, upstream.requests, "the initial request may be followed by three retries")
			require.Len(t, completed.ExecutionInfo.Attempts, 4)
			for _, attempt := range completed.ExecutionInfo.Attempts[:3] {
				require.Equal(t, 401, attempt.HTTPStatus)
			}
			if recover {
				require.Equal(t, "success", completed.Status)
				require.Empty(t, accounts.errorCalls)
				require.Equal(t, StatusActive, accounts.account.Status)
			} else {
				require.Equal(t, "failed", completed.Status)
				require.Len(t, accounts.errorCalls, 1, "only the exhausted authentication failure is persisted")
				require.Equal(t, StatusError, accounts.account.Status)
			}
			requireAutomaticRetrySingleRow(t, results.retryResultRepoStub, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryStillHonorsManualStopAndCancellation(t *testing.T) {
	for _, interruption := range []string{"manual stop", "cancel"} {
		t.Run(interruption, func(t *testing.T) {
			plan := automaticRetryTestPlan()
			accounts := &scheduledTestRetryStateAccountRepo{account: &Account{
				ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
			}}
			results := &scheduledTestRetryStateResultRepo{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}, accounts: accounts}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
				costJSONResponse(401, `{"error":{"message":"invalid key"}}`),
			}, beforeReturn: func(_ *http.Request, _ int) {
				if interruption == "manual stop" {
					accounts.stopScheduling()
				} else {
					cancel()
				}
			}}
			runner := automaticRetryTestRunner(results, accounts)
			defer runner.Stop()
			runner.accountTestSvc.httpUpstream = upstream
			runner.accountTestSvc.cfg = &config.Config{}
			runner.runOnePlan(ctx, plan)
			completed := automaticRetryCompleted(t, results.retryResultRepoStub)
			require.Equal(t, 1, upstream.requests, "an interruption must prevent the next upstream request")
			require.Equal(t, "failed", completed.Status)
			require.Empty(t, accounts.errorCalls)
			require.Len(t, completed.ExecutionInfo.Attempts, 1)
			if interruption == "cancel" {
				require.Equal(t, "canceled", completed.ExecutionInfo.FailureKind)
			} else {
				require.Contains(t, completed.ErrorMessage, "account is no longer enabled for detection")
			}
			requireAutomaticRetrySingleRow(t, results.retryResultRepoStub, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryRetriesOutputContractFailures(t *testing.T) {
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
			runner.runOnePlan(context.Background(), automaticRetryTestPlan())
			completed := automaticRetryCompleted(t, results)
			require.Equal(t, int32(4), calls.Load(), "invalid output receives three automatic retries")
			require.Equal(t, "failed", completed.Status)
			require.Contains(t, completed.ErrorMessage, "expected")
			requireAutomaticRetrySingleRow(t, results, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryRecoversOutputContractAndKeepsAttempts(t *testing.T) {
	for _, tc := range []struct {
		kind, output string
	}{{"html", "<html><body>complete output</body></html>"}, {"number", "21"}} {
		t.Run(tc.kind, func(t *testing.T) {
			results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
			plan := automaticRetryTestPlan()
			account := &Account{ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
				Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
				Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}
			accounts := &modelIdentityAccountRepo{account: account}
			upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{
				costJSONResponse(200, `{"model":"test-model","status":"completed","usage":{"input_tokens":3,"output_tokens":2},"output":[{"type":"message","content":[{"type":"output_text","text":"ordinary text"}]}]}`),
				costJSONResponse(200, fmt.Sprintf(`{"model":"test-model","status":"completed","usage":{"input_tokens":5,"output_tokens":4},"output":[{"type":"message","content":[{"type":"output_text","text":%q}]}]}`, tc.output)),
			}}
			runner := automaticRetryTestRunner(results, accounts)
			defer runner.Stop()
			runner.accountTestSvc.httpUpstream = upstream
			runner.accountTestSvc.cfg = &config.Config{}
			runner.scheduledSvc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, Prompt: "produce structured output", OutputKind: tc.kind},
			}})
			runner.runOnePlan(context.Background(), plan)
			completed := automaticRetryCompleted(t, results)
			require.Equal(t, 2, upstream.requests)
			require.Equal(t, "success", completed.Status)
			require.Contains(t, completed.ResponseText, tc.output)
			require.Empty(t, completed.ErrorMessage)
			require.Len(t, completed.ExecutionInfo.Attempts, 2)
			require.Equal(t, "output_invalid", completed.ExecutionInfo.Attempts[0].FailureKind)
			require.Empty(t, completed.ExecutionInfo.Attempts[1].FailureKind)
			require.EqualValues(t, 8, *completed.ExecutionInfo.InputTokens)
			require.EqualValues(t, 6, *completed.ExecutionInfo.OutputTokens)
			requireAutomaticRetrySingleRow(t, results, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryHandlesMissingResult(t *testing.T) {
	for _, recover := range []bool{false, true} {
		t.Run(fmt.Sprint(recover), func(t *testing.T) {
			calls := 0
			runner := &ScheduledTestRunnerService{automaticRetryBaseDelay: time.Millisecond}
			result := runner.runWithAutomaticRetries(context.Background(), &ScheduledTestPlan{}, nil, func(context.Context) *ScheduledTestResult {
				calls++
				if recover && calls == 2 {
					return &ScheduledTestResult{Status: "success", ResponseText: "recovered"}
				}
				return nil
			})
			if recover {
				require.Equal(t, 2, calls, "a successful retry stops the remaining attempts")
				require.Equal(t, "success", result.Status)
				require.Empty(t, result.ExecutionInfo.FailureKind)
			} else {
				require.Equal(t, 4, calls, "a missing result receives three automatic retries")
				require.Equal(t, "failed", result.Status)
				require.Equal(t, "unknown", result.ExecutionInfo.FailureKind)
				require.Contains(t, result.ErrorMessage, "test returned no result")
			}
		})
	}
}

func TestScheduledTestAutomaticRetryDoesNotRetryWrongNumericAnswer(t *testing.T) {
	plan := protectionPlan()
	plan.Protection.Rules[0].ExpectedAnswer = "21"
	plan.Protection.Rules[0].AnswerMatch = "numeric"
	repo := &protectionRepositoryStub{eligible: true}
	accounts := &modelIdentityAccountRepo{account: &Account{
		ID: *plan.AccountID, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true},
	}}
	upstream := &modelIdentityHTTPUpstream{body: `{"model":"gpt-5.6-sol","status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"29"}]}]}`}
	accountTests := &AccountTestService{accountRepo: accounts, httpUpstream: upstream, cfg: &config.Config{}}
	runner := NewScheduledTestRunnerService(nil, NewScheduledTestService(nil, repo), accountTests, accounts, nil, nil)
	runner.automaticRetryBaseDelay = time.Millisecond
	defer runner.Stop()
	runner.runOneAccount(context.Background(), plan, *plan.AccountID, "count the candies", "number")
	require.Equal(t, 1, upstream.requests, "a completed wrong answer is a quality failure, not an execution failure")
	require.Equal(t, "success", repo.completed.Status)
	require.Equal(t, 29.0, *repo.completed.OutputNumeric)
	require.Equal(t, "fail", repo.verdict)
	require.Equal(t, []string{"create", "begin", "update", "complete"}, repo.events)
}

func TestScheduledTestAutomaticRetrySkipsLaterDefinitionsAfterExhaustion(t *testing.T) {
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
	runner.runOnePlan(context.Background(), plan)
	first := automaticRetryCompleted(t, results)
	second := automaticRetryCompleted(t, results)
	require.Equal(t, int32(4), calls.Load(), "invalid HTML stops this account after exhausting its three retries")
	require.Equal(t, int64(1), *first.TestDefinitionID)
	require.Equal(t, "failed", first.Status)
	require.Contains(t, first.ErrorMessage, "expected HTML")
	require.Equal(t, int64(2), *second.TestDefinitionID)
	require.Equal(t, "failed", second.Status)
	require.Contains(t, second.ErrorMessage, "前一项检测未满足条件")
	require.Equal(t, 2, results.countCreated(), "each configured type retains its own single execution record")
	require.Equal(t, results.created[0].ID, first.ID)
	require.Equal(t, results.created[1].ID, second.ID)
	require.Empty(t, results.updated)
	require.Equal(t, 1, runner.planRepo.(*runnerPlanRepoStub).updated, "one multi-type rule advances its schedule once")
}

func TestScheduledTestCompatibilityCombinationShortCircuitsOnlyImpossibleBranches(t *testing.T) {
	for _, operator := range []string{"all", "any"} {
		t.Run(operator, func(t *testing.T) {
			results := &protectionRepositoryStub{eligible: true}
			var calls atomic.Int32
			accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
				calls.Add(1)
				return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
			}}
			plans := &runnerPlanRepoStub{}
			svc := NewScheduledTestService(plans, results)
			svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true, Prompt: "first", OutputKind: "text"},
				2: {ID: 2, Enabled: true, Prompt: "second", OutputKind: "text"},
			}})
			runner := NewScheduledTestRunnerService(plans, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
			runner.automaticRetryBaseDelay = time.Millisecond
			runner.executionTimeout = time.Second
			defer runner.Stop()
			plan := automaticRetryTestPlan()
			plan.Enabled = true
			plan.TestDefinitionIDs = []int64{1, 2}
			plan.Protection = ScheduledTestProtectionConfig{
				Enabled: true, Mode: "combined",
				Rules: []ScheduledTestProtectionRule{
					{TestDefinitionID: 1, ExpectedAnswer: "must-not-match"},
					{TestDefinitionID: 2},
				},
				Combinations: []ScheduledTestCombinationRule{{Condition: ScheduledTestCombinationCondition{
					Operator: operator,
					Conditions: []ScheduledTestCombinationCondition{
						{Operator: "test", TestDefinitionID: 1, Verdict: "pass"},
						{Operator: "test", TestDefinitionID: 2, Verdict: "pass"},
					},
				}}},
			}
			runner.runOnePlan(context.Background(), plan)
			second := results.completed
			require.Equal(t, int64(2), *second.TestDefinitionID)
			if operator == "all" {
				require.EqualValues(t, 1, calls.Load(), "the second upstream check is unreachable after an impossible all-condition")
				require.Equal(t, "failed", second.Status)
				require.Equal(t, "检测跳过：前一项检测未满足条件", second.ErrorMessage)
			} else {
				require.EqualValues(t, 2, calls.Load(), "both branches remain possible until the second check")
				require.Equal(t, "success", second.Status)
			}
		})
	}
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
	runner.RunPlanNow(context.Background(), automaticRetryTestPlan())
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
			accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
				calls.Add(1)
				return &Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey,
					Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com"},
					Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: true}}, nil
			}}
			runner := automaticRetryTestRunner(results, accounts)
			upstream := &modelIdentityHTTPUpstream{responses: []*http.Response{costJSONResponse(503, `{"error":{"type":"server_error"}}`)}, beforeReturn: func(*http.Request, int) { entered <- struct{}{} }}
			runner.accountTestSvc.httpUpstream = upstream
			runner.accountTestSvc.cfg = &config.Config{}
			runner.automaticRetryBaseDelay = time.Hour
			runner.workerSem = make(chan struct{}, 1)
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan struct{})
			go func() {
				defer close(done)
				runner.runOnePlan(ctx, automaticRetryTestPlan())
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
			select {
			case <-done:
				t.Fatal("transient failure did not enter the retry delay")
			default:
			}

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
			require.Equal(t, "canceled", completed.ExecutionInfo.FailureKind)
			require.Len(t, completed.ExecutionInfo.Attempts, 1)
			requireAutomaticRetrySingleRow(t, results, completed)
		})
	}
}

func TestScheduledTestAutomaticRetryAppliesToManualRunAndSingleResultRetry(t *testing.T) {
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
		require.Equal(t, int32(4), calls.Load(), "manual runs receive three automatic retries after an execution error")
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
		require.Equal(t, int32(5), calls.Load(), "one validation followed by four execution attempts")
		require.Equal(t, previous.ID, pending.ID)
		require.Equal(t, previous.ID, completed.ID)
		require.Equal(t, "failed", completed.Status)
		require.Zero(t, results.countCreated())
		require.Empty(t, results.updated)
	})
}

func TestScheduledTestAutomaticRetryCronDiscoveryRetriesExecutionErrors(t *testing.T) {
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 5)}
	var calls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
		calls.Add(1)
		return nil, errors.New("scheduled test failure")
	}}
	runner := automaticRetryTestRunner(results, accounts)
	defer runner.Stop()
	runner.planRepo = &scheduledTestDiscoveryPlanRepo{due: []*ScheduledTestPlan{automaticRetryTestPlan()}}
	// Admitted cron work must survive cancellation of the discovery request.
	ctx, cancel := context.WithCancel(context.Background())
	runner.runDuePlans(ctx)
	cancel()
	completed := automaticRetryCompleted(t, results)
	runner.activeRuns.Wait()
	require.Equal(t, int32(4), calls.Load(), "admitted cron work outlives discovery and receives three retries")
	require.Equal(t, "failed", completed.Status)
	requireAutomaticRetrySingleRow(t, results, completed)
}
