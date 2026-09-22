package service

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type snapshotResultRepoStub struct {
	*retryResultRepoStub
	targets   []int64
	blocked   map[int64]bool
	initErr   error
	queries   atomic.Int32
	published chan []*ScheduledTestResult
}

func (r *snapshotResultRepoStub) ListPlanTargetAccountIDs(context.Context, *ScheduledTestPlan, *int64) ([]int64, error) {
	r.queries.Add(1)
	return r.targets, nil
}

func (r *snapshotResultRepoStub) ListPlanDetectionAccountIDs(_ context.Context, _ *ScheduledTestPlan, accountID *int64) ([]int64, error) {
	if r.blocked[*accountID] {
		return nil, nil
	}
	return []int64{*accountID}, nil
}

func (r *snapshotResultRepoStub) BeginProtectionRun(context.Context, *ScheduledTestPlan, time.Time) error {
	return r.initErr
}

func (r *snapshotResultRepoStub) BeginRun(ctx context.Context, planID int64, runID string, inputs []*ScheduledTestResult) ([]*ScheduledTestResult, error) {
	var results, snapshots []*ScheduledTestResult
	for _, input := range inputs {
		input.RunID = runID
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

func (*snapshotResultRepoStub) CollectStatistics(context.Context, ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
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
		require.Equal(t, "failed", finals[[2]int64{13, id}].Status)
		require.Contains(t, finals[[2]int64{13, id}].ErrorMessage, "检测跳过")
	}
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
