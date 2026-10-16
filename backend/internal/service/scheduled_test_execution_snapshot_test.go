package service

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type snapshotResultRepoStub struct {
	*retryResultRepoStub
	targets                  []int64
	blocked                  map[int64]bool
	initErr                  error
	queries                  atomic.Int32
	published                chan []*ScheduledTestResult
	latestRun                string
	frozenMembers            map[int64]bool
	forbiddenMembershipReads atomic.Int32
	collect                  func(context.Context, ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error)
}

func (r *snapshotResultRepoStub) ListPlanTargetAccountIDs(context.Context, *ScheduledTestPlan, *int64) ([]int64, error) {
	r.queries.Add(1)
	return r.targets, nil
}

func (r *snapshotResultRepoStub) ListPlanDetectionAccountIDs(_ context.Context, _ *ScheduledTestPlan, accountID *int64) ([]int64, error) {
	r.forbiddenMembershipReads.Add(1)
	if r.blocked[*accountID] {
		return nil, nil
	}
	return []int64{*accountID}, nil
}

func (r *snapshotResultRepoStub) IsPlanRunAccountEligible(_ context.Context, _ int64, runID string, accountID int64) (bool, error) {
	return runID == r.latestRun && r.frozenMembers[accountID] && !r.blocked[accountID], nil
}

func (r *snapshotResultRepoStub) BeginProtectionRun(context.Context, *ScheduledTestPlan, time.Time) error {
	return r.initErr
}

func (r *snapshotResultRepoStub) BeginRun(ctx context.Context, plan *ScheduledTestPlan, runID string, inputs []*ScheduledTestResult) ([]*ScheduledTestResult, error) {
	r.latestRun = runID
	r.frozenMembers = make(map[int64]bool)
	var results, snapshots []*ScheduledTestResult
	for _, input := range inputs {
		input.RunID = runID
		if input.AccountID != nil {
			r.frozenMembers[*input.AccountID] = true
		}
		result, err := r.Create(ctx, input)
		if err != nil {
			return nil, err
		}
		results = append(results, result)
		copy := *result
		snapshots = append(snapshots, &copy)
	}
	if r.published != nil {
		r.published <- snapshots
	}
	return results, nil
}

func (r *snapshotResultRepoStub) CollectStatistics(ctx context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
	if r.collect != nil {
		return r.collect(ctx, filter)
	}
	return &ScheduledTestStatistics{TotalRequests: 10, SuccessRequests: 10}, nil
}
func (r *snapshotResultRepoStub) ListStatisticsAccountIDs(context.Context, int64) ([]int64, error) {
	return r.targets, nil
}

func newSnapshotRunner(repo *snapshotResultRepoStub, accounts AccountRepository) *ScheduledTestRunnerService {
	runner := newScheduledTestExecutionRunner(repo, accounts)
	runner.scheduledSvc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "first", OutputKind: "text"},
		2: {ID: 2, Enabled: true, Prompt: "second", OutputKind: "text"},
		3: {ID: 3, Enabled: true, OutputKind: "statistics"},
	}})
	return runner
}

func TestScheduledTestSnapshotPersistsAllQueuedTargetsAndClosesThemOnCancellation(t *testing.T) {
	repo := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 100)}, published: make(chan []*ScheduledTestResult, 1)}
	for id := int64(1); id <= 12; id++ {
		repo.targets = append(repo.targets, id)
	}
	entered := make(chan struct{}, 20)
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		entered <- struct{}{}
		<-ctx.Done()
		return nil, ctx.Err()
	}}
	runner := newSnapshotRunner(repo, accounts)
	runner.executionTimeout = time.Second
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runner.RunPlanNow(ctx, plan) }()
	var snapshots []*ScheduledTestResult
	select {
	case snapshots = <-repo.published:
	case <-time.After(3 * time.Second):
		t.Fatal("snapshot not published")
	}
	require.Len(t, snapshots, 24)
	for _, result := range snapshots {
		require.Equal(t, "pending", result.Status)
		require.NotEmpty(t, result.RunID)
	}
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("upstream never started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("cancellation did not complete")
	}
	close(repo.updated)
	finals := map[int64]*ScheduledTestResult{}
	for result := range repo.updated {
		if result.Status == "failed" {
			finals[result.ID] = result
		}
	}
	require.Len(t, finals, 24, "cancellation must not leave later accounts/types without a terminal result")
	require.EqualValues(t, 1, repo.queries.Load(), "one frozen account list per run")
}

func TestScheduledTestSnapshotIncludesStatisticsAndEverySkippedAccountType(t *testing.T) {
	repo := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 100)}, targets: []int64{12, 13}, blocked: map[int64]bool{13: true}}
	accounts := multiDefinitionAccountRepoStub{accounts: []Account{{ID: 12, Extra: map[string]any{"synthetic_ui_test": true}}}}
	runner := newSnapshotRunner(repo, accounts)
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	plan.TestDefinitionIDs = []int64{1, 2, 3}
	runner.RunPlanNow(context.Background(), plan)
	close(repo.updated)
	finals := map[[2]int64]*ScheduledTestResult{}
	for result := range repo.updated {
		if result.Status != "running" {
			finals[[2]int64{*result.AccountID, *result.TestDefinitionID}] = result
		}
	}
	require.Len(t, finals, 6)
	for _, id := range plan.TestDefinitionIDs {
		require.Equal(t, "success", finals[[2]int64{12, id}].Status)
		require.Equal(t, repo.latestRun, finals[[2]int64{12, id}].RunID, "all result kinds must retain their frozen round")
		require.Equal(t, "failed", finals[[2]int64{13, id}].Status)
		require.Contains(t, finals[[2]int64{13, id}].ErrorMessage, "检测跳过")
	}
}

func TestScheduledTestSnapshotStopsOnlyTheFailedAccount(t *testing.T) {
	repo := &snapshotResultRepoStub{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 16)},
		targets:             []int64{12, 13},
	}
	var failedCalls, passingCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		if id == 12 {
			failedCalls.Add(1)
			return nil, errors.New("upstream unavailable")
		}
		passingCalls.Add(1)
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	runner.RunPlanNow(context.Background(), plan)
	close(repo.updated)

	finals := make(map[[2]int64]*ScheduledTestResult)
	for result := range repo.updated {
		if result.Status != "running" {
			finals[[2]int64{*result.AccountID, *result.TestDefinitionID}] = result
		}
	}
	require.Len(t, finals, 4, "all snapshot rows need a terminal result")
	require.Equal(t, "failed", finals[[2]int64{12, 1}].Status)
	require.Equal(t, "failed", finals[[2]int64{12, 2}].Status)
	require.Contains(t, finals[[2]int64{12, 2}].ErrorMessage, "前一项检测未满足条件")
	require.Equal(t, "success", finals[[2]int64{13, 1}].Status)
	require.Equal(t, "success", finals[[2]int64{13, 2}].Status)
	require.EqualValues(t, 4, failedCalls.Load(), "the first check finishes its normal retry budget")
	require.EqualValues(t, 2, passingCalls.Load(), "another account still runs both checks")
}

func TestScheduledTestSnapshotRunsCheapStatisticsFirstAndSkipsFailedAccount(t *testing.T) {
	repo := &snapshotResultRepoStub{
		retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 16)},
		targets:             []int64{12, 13},
	}
	repo.collect = func(_ context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
		if filter.AccountID != nil && *filter.AccountID == 12 {
			return nil, errors.New("statistics unavailable")
		}
		return &ScheduledTestStatistics{TotalRequests: 10, SuccessRequests: 10}, nil
	}
	var upstreamCalls, failedAccountCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		upstreamCalls.Add(1)
		if id == 12 {
			failedAccountCalls.Add(1)
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	plan.TestDefinitionIDs = []int64{1, 3} // The costly check was configured first.
	runner.RunPlanNow(context.Background(), plan)
	close(repo.updated)

	finals := make(map[[2]int64]*ScheduledTestResult)
	for result := range repo.updated {
		if result.Status != "running" {
			finals[[2]int64{*result.AccountID, *result.TestDefinitionID}] = result
		}
	}
	require.Len(t, finals, 4)
	require.Equal(t, "failed", finals[[2]int64{12, 3}].Status)
	require.Equal(t, "failed", finals[[2]int64{12, 1}].Status)
	require.Equal(t, "检测跳过：前一项检测未满足条件", finals[[2]int64{12, 1}].ErrorMessage)
	require.Equal(t, "success", finals[[2]int64{13, 3}].Status)
	require.Equal(t, "success", finals[[2]int64{13, 1}].Status)
	require.Zero(t, failedAccountCalls.Load(), "the account with failed statistics must not contact upstream")
	require.EqualValues(t, 1, upstreamCalls.Load())
}

func TestScheduledTestResultSatisfiesConfiguredCondition(t *testing.T) {
	plan := scheduledTestExecutionPlan()
	plan.Enabled = true
	plan.TestDefinitionID = scheduledTestPtrInt64(1)
	plan.Protection = ScheduledTestProtectionConfig{
		Enabled: true, Mode: "all_pass",
		Rules: []ScheduledTestProtectionRule{{TestDefinitionID: 1, ExpectedAnswer: "21", AnswerMatch: "numeric"}},
	}
	wrong, right := 20.0, 21.0
	result := &ScheduledTestResult{Status: "success", OutputKind: "number", OutputNumeric: &wrong}
	require.False(t, scheduledTestResultSatisfies(plan, result), "a completed but wrong answer must stop later checks")
	result.OutputNumeric = &right
	require.True(t, scheduledTestResultSatisfies(plan, result))
	result.OutputNumeric = nil
	require.False(t, scheduledTestResultSatisfies(plan, result), "an inconclusive automatic condition cannot clear the gate")

	result.Status, result.OutputKind = "success", "text"
	plan.TestDefinitionID = scheduledTestPtrInt64(2) // no rule: display only
	require.True(t, scheduledTestResultSatisfies(plan, result), "successful display-only checks do not gate the strategy")
	result.Status = "failed"
	require.True(t, scheduledTestResultSatisfies(plan, result), "display-only failures do not stop the all-pass queue")
	plan.TestDefinitionID = scheduledTestPtrInt64(1)
	require.False(t, scheduledTestResultSatisfies(plan, result), "failed automatic checks still stop later checks")
}

func TestScheduledTestCombinedConditionKeepsPossibleOrBranch(t *testing.T) {
	plan := scheduledTestExecutionPlan()
	plan.Enabled = true
	plan.Protection = ScheduledTestProtectionConfig{
		Enabled: true, Mode: "combined",
		Rules: []ScheduledTestProtectionRule{
			{TestDefinitionID: 1, ExpectedAnswer: "yes"},
			{TestDefinitionID: 2, ExpectedAnswer: "yes"},
		},
		Combinations: []ScheduledTestCombinationRule{{
			Condition: ScheduledTestCombinationCondition{Operator: "any", Conditions: []ScheduledTestCombinationCondition{
				{Operator: "test", TestDefinitionID: 1, Verdict: "pass"},
				{Operator: "test", TestDefinitionID: 2, Verdict: "pass"},
			}},
		}},
	}
	var progress scheduledTestAccountProgress
	plan.TestDefinitionID = scheduledTestPtrInt64(1)
	require.True(t, progress.allowsNext(plan, &ScheduledTestResult{Status: "success", ResponseText: "no"}), "the second branch can still pass")
	plan.TestDefinitionID = scheduledTestPtrInt64(2)
	require.True(t, progress.allowsNext(plan, &ScheduledTestResult{Status: "success", ResponseText: "yes"}))

	plan.Protection.Combinations[0].Condition.Operator = "all"
	plan.TestDefinitionID = scheduledTestPtrInt64(1)
	progress = scheduledTestAccountProgress{}
	require.False(t, progress.allowsNext(plan, &ScheduledTestResult{Status: "success", ResponseText: "no"}), "an impossible AND branch can stop immediately")
}

func TestScheduledTestSnapshotRecordsProtectionInitializationFailureForEveryTarget(t *testing.T) {
	repo := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 20)}, targets: []int64{12}, initErr: errors.New("plan changed")}
	runner := newSnapshotRunner(repo, nil)
	plan := scheduledTestExecutionPlan()
	plan.Protection.Enabled = true
	runner.RunPlanNow(context.Background(), plan)
	close(repo.updated)
	var count int
	for result := range repo.updated {
		count++
		require.Equal(t, "failed", result.Status)
		require.Contains(t, result.ErrorMessage, "initialize test protection")
	}
	require.Equal(t, 2, count)
}

func TestScheduledTestSnapshotFastAccountAdvancesWhileAnotherWaits(t *testing.T) {
	repo := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 20)}, targets: []int64{12, 13}}
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSlow) })
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		if id == 12 {
			select {
			case <-releaseSlow:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	runner.executionTimeout = time.Minute
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	done := make(chan struct{})
	go func() { defer close(done); runner.RunPlanNow(ctx, plan) }()

	completed := map[[2]int64]bool{}
	consume := func(result *ScheduledTestResult) {
		accountID, definitionID := *result.AccountID, *result.TestDefinitionID
		if result.Status == "running" && definitionID == 2 {
			require.True(t, completed[[2]int64{accountID, 1}], "each account's first result must be saved before starting its next type")
		}
		if result.Status == "success" {
			completed[[2]int64{accountID, definitionID}] = true
		}
	}
	timeout := time.NewTimer(3 * time.Second)
	defer timeout.Stop()
	for !completed[[2]int64{13, 2}] {
		select {
		case result := <-repo.updated:
			consume(result)
		case <-timeout.C:
			t.Fatal("fast account's second test waited for the slow account's first test")
		}
	}
	require.False(t, completed[[2]int64{12, 1}])
	releaseOnce.Do(func() { close(releaseSlow) })
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("remaining account queue did not complete")
	}
	close(repo.updated)
	for result := range repo.updated {
		consume(result)
	}
	require.Len(t, completed, 4)
	require.EqualValues(t, 1, repo.queries.Load(), "moving an account must not change this round's frozen target list")
}

func TestScheduledTestSnapshotFastAccountAdvancesPastAnotherAccountsStatistics(t *testing.T) {
	repo := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 16)}, targets: []int64{12, 13}}
	releaseSlow := make(chan struct{})
	var releaseOnce sync.Once
	defer releaseOnce.Do(func() { close(releaseSlow) })
	repo.collect = func(ctx context.Context, filter ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
		if filter.AccountID != nil && *filter.AccountID == 12 {
			select {
			case <-releaseSlow:
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return &ScheduledTestStatistics{TotalRequests: 10, SuccessRequests: 10}, nil
	}
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	runner.executionTimeout = time.Minute
	plan := scheduledTestExecutionPlan()
	plan.AccountID, plan.GroupID, plan.TargetMode = nil, scheduledTestPtrInt64(8), "all_accounts"
	plan.TestDefinitionIDs = []int64{1, 3} // Local statistics still run first for each account.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan struct{})
	go func() { defer close(done); runner.RunPlanNow(ctx, plan) }()
	deadline := time.NewTimer(3 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case result := <-repo.updated:
			if result.Status == "success" && result.AccountID != nil && *result.AccountID == 13 && *result.TestDefinitionID == 1 {
				select {
				case <-done:
					t.Fatal("the slow account should still be collecting statistics")
				default:
				}
				releaseOnce.Do(func() { close(releaseSlow) })
				select {
				case <-done:
				case <-time.After(3 * time.Second):
					t.Fatal("the slow account did not finish after statistics were released")
				}
				return
			}
		case <-deadline.C:
			t.Fatal("the fast account waited for another account's statistics")
		}
	}
}

// Membership changes can happen as soon as a first result is reconciled. The
// second test must use the frozen round, and new arrivals wait for a fresh run.
func TestScheduledTestSnapshotDeduplicatesAndIgnoresMidRoundMembershipChanges(t *testing.T) {
	repo := &snapshotResultRepoStub{retryResultRepoStub: &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 30)}, targets: []int64{12, 12}}
	var reads atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(ctx context.Context, id int64) (*Account, error) {
		reads.Add(1)
		repo.targets = []int64{13}
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newSnapshotRunner(repo, accounts)
	plan := scheduledTestExecutionPlan()
	plan.GroupIDs = []int64{8, 10}
	plan.AccountID = nil
	plan.TargetMode = "all_accounts"
	runner.RunPlanNow(context.Background(), plan)
	require.EqualValues(t, 2, reads.Load(), "one upstream execution per selected test, despite duplicate group membership")
	require.EqualValues(t, 1, repo.queries.Load())
	require.Zero(t, repo.forbiddenMembershipReads.Load(), "eligibility must use persisted snapshot, not live source groups")
	require.True(t, repo.frozenMembers[12])
	require.False(t, repo.frozenMembers[13])
	firstRun := repo.latestRun
	runner.RunPlanNow(context.Background(), plan)
	require.NotEqual(t, firstRun, repo.latestRun)
	require.True(t, repo.frozenMembers[13], "newly joined accounts are admitted next round")
	require.False(t, repo.frozenMembers[12])
	require.EqualValues(t, 4, reads.Load())
	close(repo.updated)
	finals := map[string]map[[2]int64]int{}
	for result := range repo.updated {
		if result.Status != "success" {
			continue
		}
		require.NotEmpty(t, result.RunID, "completion must retain snapshot identity for protection")
		if finals[result.RunID] == nil {
			finals[result.RunID] = map[[2]int64]int{}
		}
		finals[result.RunID][[2]int64{*result.AccountID, *result.TestDefinitionID}]++
	}
	require.Len(t, finals, 2)
	for _, pairs := range finals {
		require.Len(t, pairs, 2)
		for _, count := range pairs {
			require.Equal(t, 1, count)
		}
	}
}
