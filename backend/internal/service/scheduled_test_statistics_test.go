package service

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type statisticsResultRepoStub struct {
	*retryResultRepoStub
	statisticsMu sync.Mutex
	filters      []ScheduledTestStatisticsFilter
	listedGroups []int64
	accountIDs   []int64
	collect      func(context.Context, ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error)
}

func (r *statisticsResultRepoStub) ListStatisticsAccountIDs(_ context.Context, groupID int64) ([]int64, error) {
	r.statisticsMu.Lock()
	defer r.statisticsMu.Unlock()
	r.listedGroups = append(r.listedGroups, groupID)
	return append([]int64(nil), r.accountIDs...), nil
}

func (r *statisticsResultRepoStub) CollectStatistics(ctx context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
	r.statisticsMu.Lock()
	r.filters = append(r.filters, filter)
	r.statisticsMu.Unlock()
	if r.collect != nil {
		return r.collect(ctx, filter)
	}
	return &ScheduledTestStatistics{
		WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd,
		RecentRequests: []ScheduledTestRecentRequest{
			{Success: true, CreatedAt: filter.WindowEnd.Add(-time.Minute)},
			{Success: false, CreatedAt: filter.WindowEnd.Add(-2 * time.Minute)},
		},
	}, ctx.Err()
}

type statisticsReadResultsStub struct{ scheduledTestReadResultsStub }

func (r statisticsReadResultsStub) ListVisibleHistory(context.Context, int64, int64, int64, int) ([]*ScheduledTestResult, error) {
	return r.rows, nil
}

func statisticsTestService(repo ScheduledTestResultRepository) *ScheduledTestService {
	svc := NewScheduledTestService(nil, repo)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, OutputKind: "statistics"},
		2: {ID: 2, Enabled: true, OutputKind: "text", Prompt: "slow upstream check"},
	}})
	return svc
}

func statisticsTestPlan() *ScheduledTestPlan {
	return &ScheduledTestPlan{
		ID: 7, GroupID: scheduledTestPtrInt64(8), TargetMode: "group", TestDefinitionID: scheduledTestPtrInt64(1),
		ModelID: "gpt-6-astra", ReasoningEffort: "high", CronExpression: "* * * * *", MaxResults: 10, AutoRecover: true,
	}
}

func TestScheduledTestStatisticsAutomaticRetryKeepsOneResultAndWindow(t *testing.T) {
	repo := &statisticsResultRepoStub{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 1)},
	}
	runner := NewScheduledTestRunnerService(nil, statisticsTestService(repo), nil, nil, nil, nil)
	runner.automaticRetryBaseDelay = time.Millisecond
	runner.statisticsSem = make(chan struct{}, 1)
	attempts := 0
	repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
		attempts++
		require.Len(t, runner.statisticsSem, 1, "each attempt must acquire its own SQL worker")
		if attempts == 1 {
			return nil, errors.New("temporary database error")
		}
		return &ScheduledTestStatistics{WindowStart: filter.WindowStart, WindowEnd: filter.WindowEnd}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ctx = context.WithValue(ctx, scheduledTestAutomaticRunKey{}, true)
	runner.runPlanDefinition(ctx, statisticsTestPlan())
	require.NoError(t, ctx.Err())
	require.Equal(t, 2, attempts)
	require.Empty(t, runner.statisticsSem, "retry must release SQL capacity")
	require.Equal(t, 1, repo.countCreated(), "automatic retries must reuse the running result")
	require.Len(t, repo.updated, 1, "only the final result is persisted")
	require.Len(t, repo.filters, 2)
	require.Equal(t, repo.filters[0], repo.filters[1], "retry must retain the original rolling-hour window")
	completed := <-repo.updated
	require.Equal(t, "success", completed.Status)
	require.Equal(t, repo.created[0].ID, completed.ID)
	require.Nil(t, completed.AccountID)
	require.Empty(t, completed.ErrorMessage)
	require.NotNil(t, completed.OutputStatistics)
	require.NotEmpty(t, completed.ResponseText)
}

func TestScheduledTestStatisticsDefinitionAllowsEmptyPromptOnlyForStatistics(t *testing.T) {
	for _, outputKind := range []string{" statistics ", "html", "text", "number", ""} {
		t.Run(outputKind, func(t *testing.T) {
			definition := &ScheduledTestDefinition{Key: "hourly_stats", Name: "Last hour", OutputKind: outputKind, Prompt: "  "}
			err := ValidateScheduledTestDefinitionInput(definition)
			if outputKind == " statistics " {
				require.NoError(t, err)
				require.Equal(t, "statistics", definition.OutputKind)
				require.Empty(t, definition.Prompt)
			} else {
				require.ErrorContains(t, err, "prompt is required")
			}
		})
	}
}

func TestScheduledTestStatisticsReadExposesOnlyTypedSnapshot(t *testing.T) {
	for _, view := range []string{"admin", "user", "history"} {
		t.Run(view, func(t *testing.T) {
			oldNumeric := 99.0
			rows := []*ScheduledTestResult{
				{ID: 11, OutputKind: " statistics ", Status: "success", ReasoningEffort: "high", OutputHTML: "secret-html", OutputNumeric: &oldNumeric,
					ResponseText: `{"window_start":"2026-09-21T10:00:00Z","window_end":"2026-09-21T11:00:00Z","total_requests":10,"success_requests":8,"failed_requests":2,"success_rate":0.8,"cache_rate":0,"avg_first_token_ms":120,"first_token_samples":8,"cache_read_tokens":0,"cache_input_tokens":100,"credentials":"secret-credential","account_name":"secret-name","recent_requests":[{"success":false,"created_at":"2026-09-21T10:59:00Z","credentials":"secret-recent-credential","request_id":"secret-request","user_email":"secret-user"},{"success":true,"created_at":"2026-09-21T10:58:00Z","user_id":12345}]}`},
				{ID: 10, OutputKind: "statistics", Status: "success", ResponseText: `not valid JSON secret-invalid`, OutputStatistics: &ScheduledTestStatistics{TotalRequests: 99}},
				{ID: 9, OutputKind: "statistics", Status: "success", ResponseText: `{"window_start":"2026-09-21T12:00:00Z","window_end":"2026-09-21T11:00:00Z","private":"secret-bad-window"}`},
				{ID: 8, OutputKind: "text", Status: "success", ResponseText: "ordinary output"},
				{ID: 7, OutputKind: "statistics", Status: "success", ResponseText: `{"window_start":"2026-09-21T10:00:00Z","window_end":"2026-09-21T11:00:00Z"}`},
			}
			svc := NewScheduledTestService(nil, statisticsReadResultsStub{scheduledTestReadResultsStub{rows: rows}})
			var results []*ScheduledTestResult
			var err error
			switch view {
			case "admin":
				results, err = svc.ListResults(context.Background(), 7, 20)
			case "user":
				results, err = svc.ListVisibleResults(context.Background(), 1, 20)
			case "history":
				var history *ScheduledTestResultHistory
				history, err = svc.ListVisibleResultHistory(context.Background(), 1, 11, 0, 20)
				if history != nil {
					results = history.Items
				}
			}
			require.NoError(t, err)
			require.Len(t, results, 5)
			snapshot := results[0].OutputStatistics
			require.NotNil(t, snapshot)
			require.Equal(t, int64(10), snapshot.TotalRequests)
			require.Equal(t, 0.8, *snapshot.SuccessRate)
			require.NotNil(t, snapshot.CacheRate, "zero cache hits must remain a measured zero")
			require.Zero(t, *snapshot.CacheRate)
			require.Equal(t, 120.0, *snapshot.AvgFirstTokenMs)
			require.Len(t, snapshot.RecentRequests, 2)
			require.False(t, snapshot.RecentRequests[0].Success)
			require.True(t, snapshot.RecentRequests[1].Success)
			require.True(t, snapshot.RecentRequests[0].CreatedAt.After(snapshot.RecentRequests[1].CreatedAt))
			for _, result := range results[:3] {
				require.Empty(t, result.ResponseText)
				require.Empty(t, result.OutputHTML)
				require.Nil(t, result.OutputNumeric)
				require.Empty(t, result.ReasoningEffort)
			}
			require.Nil(t, results[1].OutputStatistics)
			require.Nil(t, results[2].OutputStatistics)
			require.Equal(t, "ordinary output", results[3].ResponseText)
			require.NotNil(t, results[4].OutputStatistics)
			require.NotNil(t, results[4].OutputStatistics.RecentRequests, "old snapshots without recent requests must return an array")
			require.Empty(t, results[4].OutputStatistics.RecentRequests)
			encoded, marshalErr := json.Marshal(results)
			require.NoError(t, marshalErr)
			require.NotContains(t, string(encoded), "secret-")
			require.NotContains(t, string(encoded), "credentials")
			require.NotContains(t, string(encoded), "request_id")
			require.NotContains(t, string(encoded), "user_email")
			require.NotContains(t, string(encoded), "user_id")
			var publicRows []struct {
				OutputStatistics struct {
					RecentRequests []map[string]json.RawMessage `json:"recent_requests"`
				} `json:"output_statistics"`
			}
			require.NoError(t, json.Unmarshal(encoded, &publicRows))
			for _, recent := range publicRows[0].OutputStatistics.RecentRequests {
				require.Len(t, recent, 2, "recent request entries must only expose the public allowlist")
				require.Contains(t, recent, "success")
				require.Contains(t, recent, "created_at")
			}
			require.NotNil(t, publicRows[4].OutputStatistics.RecentRequests, "legacy snapshots must serialize recent_requests as [], not null")
		})
	}
}

func TestScheduledTestStatisticsScopesAndAvoidsUpstreamAndRecovery(t *testing.T) {
	for _, mode := range []string{"group", "all_accounts", "account"} {
		t.Run(mode, func(t *testing.T) {
			repo := &statisticsResultRepoStub{
				retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 3)},
				accountIDs:          []int64{12, 13, 14},
			}
			var accountReads atomic.Int32
			accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
				accountReads.Add(1)
				return nil, errors.New("upstream or recovery must not access account state")
			}}
			runner := NewScheduledTestRunnerService(nil, statisticsTestService(repo), &AccountTestService{accountRepo: accounts}, accounts, &RateLimitService{accountRepo: accounts}, nil)
			// Model tests have occupied every model worker. Local database
			// aggregation must still complete through its own capacity.
			runner.workerSem = make(chan struct{}, 1)
			runner.workerSem <- struct{}{}
			plan := statisticsTestPlan()
			plan.TargetMode = mode
			wantCount := 1
			if mode == "all_accounts" {
				wantCount = 3
			}
			if mode == "account" {
				plan.AccountID = scheduledTestPtrInt64(13)
			}
			before := time.Now().UTC()
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			runner.runPlanDefinition(ctx, plan)
			require.NoError(t, ctx.Err(), "statistics waited behind an upstream model worker")
			require.Equal(t, wantCount, repo.countCreated())
			require.Len(t, repo.filters, wantCount)
			require.Zero(t, accountReads.Load(), "local statistics must never execute upstream or auto-recover accounts")
			require.Equal(t, "high", plan.ReasoningEffort, "execution must not modify the saved model-test rule")
			ids := make([]int64, 0, wantCount)
			for range wantCount {
				result := <-repo.updated
				require.Equal(t, "success", result.Status)
				require.Equal(t, "statistics", result.OutputKind)
				require.Empty(t, result.ReasoningEffort)
				require.NotNil(t, result.OutputStatistics)
				require.Len(t, result.OutputStatistics.RecentRequests, 2)
				var persisted ScheduledTestStatistics
				require.NoError(t, json.Unmarshal([]byte(result.ResponseText), &persisted))
				require.Equal(t, result.OutputStatistics.RecentRequests, persisted.RecentRequests, "the stored snapshot must retain recent success/failure markers and timestamps")
				if mode == "group" {
					require.Nil(t, result.AccountID, "group statistics describe the whole group, not a selected account")
				} else {
					require.NotNil(t, result.AccountID)
					ids = append(ids, *result.AccountID)
				}
			}
			for _, filter := range repo.filters {
				require.Equal(t, plan.GroupID, filter.GroupID)
				require.Equal(t, plan.ModelID, filter.Model)
				require.Equal(t, time.Hour, filter.WindowEnd.Sub(filter.WindowStart))
				require.False(t, filter.WindowEnd.Before(before))
				require.Equal(t, repo.filters[0].WindowEnd, filter.WindowEnd, "all accounts share the same hourly window")
				if mode == "group" {
					require.Nil(t, filter.AccountID)
				}
			}
			if mode == "all_accounts" {
				require.Equal(t, []int64{8}, repo.listedGroups)
				require.ElementsMatch(t, repo.accountIDs, ids, "use all IDs from statistics scope even when no account is schedulable")
			} else {
				require.Empty(t, repo.listedGroups)
				if mode == "account" {
					require.Equal(t, []int64{13}, ids)
				}
			}
		})
	}
}

func TestScheduledTestStatisticsExecutesBeforeSlowModelDefinition(t *testing.T) {
	repo := &statisticsResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 2)}}
	accounts := scheduledTestAccountRepoStub{accounts: []Account{{ID: 12}}}
	plans := &runnerPlanRepoStub{}
	runner := NewScheduledTestRunnerService(plans, statisticsTestService(repo), nil, accounts, nil, nil)
	runner.workerSem = make(chan struct{}, 1)
	runner.workerSem <- struct{}{}
	plan := statisticsTestPlan()
	plan.TestDefinitionIDs = []int64{2, 1} // A slow model type is configured first.
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.runOnePlan(ctx, plan)
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("test runner did not stop after cancellation")
		}
	})
	select {
	case result := <-repo.updated:
		require.Equal(t, "statistics", result.OutputKind)
		require.Equal(t, "success", result.Status)
		require.Equal(t, int64(1), *result.TestDefinitionID)
	case <-time.After(time.Second):
		t.Fatal("statistics did not complete while the earlier model test was queued")
	}
	select {
	case <-done:
		t.Fatal("the model test should still be queued behind the occupied worker")
	default:
	}
	require.Equal(t, []int64{2, 1}, plan.TestDefinitionIDs)
}

func TestScheduledTestStatisticsFailureCompletesTheRunningRow(t *testing.T) {
	for _, cause := range []error{errors.New("statistics query failed"), nil} {
		name := "missing snapshot"
		if cause != nil {
			name = cause.Error()
		}
		t.Run(name, func(t *testing.T) {
			repo := &statisticsResultRepoStub{
				retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 1)},
				collect: func(context.Context, ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
					return nil, cause
				},
			}
			runner := NewScheduledTestRunnerService(nil, statisticsTestService(repo), nil, nil, nil, nil)
			runner.runPlanDefinition(context.Background(), statisticsTestPlan())
			require.Equal(t, 1, repo.countCreated())
			completed := <-repo.updated
			require.Equal(t, "running", repo.created[0].Status)
			require.Equal(t, repo.created[0].ID, completed.ID)
			require.Equal(t, "failed", completed.Status)
			require.NotEmpty(t, completed.ErrorMessage)
			require.Nil(t, completed.OutputStatistics)
			require.Empty(t, completed.ResponseText)
		})
	}
}

func TestScheduledTestStatisticsRetryUpdatesOriginalRowAfterRequestCancellation(t *testing.T) {
	plan := statisticsTestPlan()
	plan.TargetMode = "all_accounts"
	previous := &ScheduledTestResult{
		ID: 90, PlanID: plan.ID, TestDefinitionID: plan.TestDefinitionID, TargetMode: plan.TargetMode,
		AccountID: scheduledTestPtrInt64(13), GroupID: plan.GroupID, ModelID: plan.ModelID,
		Status: "failed", OutputKind: "statistics", ErrorMessage: "query failed", ReasoningEffort: "high",
		OutputStatistics: &ScheduledTestStatistics{TotalRequests: 99}, CreatedAt: time.Now().Add(-time.Hour),
	}
	repo := &statisticsResultRepoStub{retryResultRepoStub: &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 1)}}
	var reads atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(context.Context, int64) (*Account, error) {
		reads.Add(1)
		return &Account{ID: 13, GroupIDs: []int64{8}, Status: "error", Schedulable: false}, nil
	}}
	runner := NewScheduledTestRunnerService(nil, statisticsTestService(repo), nil, accounts, &RateLimitService{accountRepo: accounts}, nil)
	runner.statisticsSem = make(chan struct{}, 1)
	runner.statisticsSem <- struct{}{}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	pending, err := runner.RetryAccount(ctx, plan, previous)
	cancel()
	<-runner.statisticsSem
	require.NoError(t, err)
	require.Equal(t, previous.ID, pending.ID)
	require.Equal(t, "running", pending.Status)
	require.Empty(t, pending.ReasoningEffort)
	require.Nil(t, pending.OutputStatistics)
	select {
	case result := <-repo.updated:
		require.Equal(t, "success", result.Status)
		require.Equal(t, previous.ID, result.ID)
		require.Equal(t, previous.CreatedAt, result.CreatedAt)
		require.Empty(t, result.ReasoningEffort)
		require.NotNil(t, result.OutputStatistics)
		require.Equal(t, int64(13), *result.AccountID)
	case <-time.After(2 * time.Second):
		t.Fatal("statistics retry did not finish after caller cancellation")
	}
	runner.activeRuns.Wait()
	require.Zero(t, repo.countCreated(), "retry must update the failed row instead of creating another record")
	require.Equal(t, int32(1), reads.Load(), "only retry validation may read account state; no upstream call or auto-recovery")
	require.Empty(t, repo.listedGroups, "retry must only aggregate the chosen account")
	require.Equal(t, "high", plan.ReasoningEffort)
}
