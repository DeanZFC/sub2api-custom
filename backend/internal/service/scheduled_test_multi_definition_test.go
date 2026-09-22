package service

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type multiDefinitionRepoStub struct {
	ScheduledTestDefinitionRepository
	definitions map[int64]*ScheduledTestDefinition
}

func (r multiDefinitionRepoStub) GetByID(_ context.Context, id int64) (*ScheduledTestDefinition, error) {
	if definition := r.definitions[id]; definition != nil {
		return definition, nil
	}
	return nil, sql.ErrNoRows
}

type multiDefinitionPlanRepoStub struct {
	ScheduledTestPlanRepository
	saved *ScheduledTestPlan
}

type multiDefinitionAccountRepoStub struct {
	AccountRepository
	accounts []Account
}

type multiDefinitionObservedResultRepo struct {
	*retryResultRepoStub
	started chan *ScheduledTestResult
}

// targetAccountResultRepo models the production split between the configured
// target list and the live eligibility check. The account is configured for
// every definition, but is deliberately ineligible, so each type must still
// leave an administrator-visible skipped result.
type targetAccountResultRepo struct {
	*runnerResultRepoStub
}

func (*targetAccountResultRepo) ListPlanTargetAccountIDs(context.Context, *ScheduledTestPlan, *int64) ([]int64, error) {
	return []int64{42}, nil
}

func (*targetAccountResultRepo) ListPlanDetectionAccountIDs(context.Context, *ScheduledTestPlan, *int64) ([]int64, error) {
	return nil, nil
}

func (r *multiDefinitionObservedResultRepo) Create(ctx context.Context, result *ScheduledTestResult) (*ScheduledTestResult, error) {
	created, err := r.retryResultRepoStub.Create(ctx, result)
	if err == nil {
		snapshot := *created
		r.started <- &snapshot
	}
	return created, err
}

func (r multiDefinitionAccountRepoStub) ListSchedulableByGroupID(context.Context, int64) ([]Account, error) {
	return r.accounts, nil
}

func (r multiDefinitionAccountRepoStub) GetByID(_ context.Context, id int64) (*Account, error) {
	for _, account := range r.accounts {
		if account.ID == id {
			return &account, nil
		}
	}
	return nil, ErrAccountNotFound
}

func (r *multiDefinitionPlanRepoStub) Create(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func (r *multiDefinitionPlanRepoStub) Update(_ context.Context, plan *ScheduledTestPlan) (*ScheduledTestPlan, error) {
	r.saved = plan
	return plan, nil
}

func TestScheduledTestMultipleDefinitionsPlanValidation(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			plans := &multiDefinitionPlanRepoStub{}
			svc := NewScheduledTestService(plans, nil)
			svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				1: {ID: 1, Enabled: true},
				2: {ID: 2, Enabled: false},
			}})
			plan := &ScheduledTestPlan{ID: 10, GroupIDs: []int64{8}, GroupID: scheduledTestPtrInt64(8), TargetMode: "all_accounts", TestDefinitionIDs: []int64{2, 1, 2}, ModelID: "model", CronExpression: "*/5 * * * *", Enabled: true}
			write := svc.CreatePlan
			if operation == "update" {
				write = svc.UpdatePlan
			}
			got, err := write(context.Background(), plan)
			require.NoError(t, err)
			require.Equal(t, []int64{2, 1}, got.TestDefinitionIDs)
			require.Equal(t, int64(2), *got.TestDefinitionID)
			require.NotNil(t, got.NextRunAt)
			require.Same(t, got, plans.saved)
			plan.TestDefinitionIDs = []int64{1, 99}
			_, err = write(context.Background(), plan)
			require.ErrorContains(t, err, "99 not found")
			plan.TestDefinitionIDs = []int64{1, 0}
			_, err = write(context.Background(), plan)
			require.ErrorContains(t, err, "positive IDs")
		})
	}
}

func TestScheduledTestDefinitionSelectionRequiresNewStrategyArrays(t *testing.T) {
	plan := &ScheduledTestPlan{AccountID: scheduledTestPtrInt64(1), ModelID: "model", CronExpression: "* * * * *"}
	require.ErrorContains(t, validateScheduledTestPlan(plan), "group_ids")
	plan.GroupIDs = []int64{8}
	plan.TestDefinitionID = scheduledTestPtrInt64(2)
	require.ErrorContains(t, validateScheduledTestPlan(plan), "test_definition_ids")
	plan.TestDefinitionIDs = []int64{2}
	require.NoError(t, validateScheduledTestPlan(plan))
	require.Nil(t, plan.AccountID)
}

func TestScheduledTestRunnerExecutesEachDefinitionAndContinuesPastDisabledType(t *testing.T) {
	plans := &runnerPlanRepoStub{}
	results := &runnerResultRepoStub{}
	svc := NewScheduledTestService(plans, results)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "first check", OutputKind: "text"},
		2: {ID: 2, Enabled: false, Prompt: "disabled check", OutputKind: "html"},
		3: {ID: 3, Enabled: true, Prompt: "third check", OutputKind: "text"},
	}})
	accountID := int64(12)
	accounts := retryAccountRepoStub{account: &Account{ID: accountID, Extra: map[string]any{"synthetic_ui_test": true}}}
	runner := NewScheduledTestRunnerService(plans, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	plan := &ScheduledTestPlan{ID: 7, AccountID: &accountID, TargetMode: "account", TestDefinitionIDs: []int64{1, 2, 3}, ModelID: "model", CronExpression: "* * * * *", MaxResults: 10}
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, 1, plans.updated, "a multi-check rule advances its cron only once")
	require.Len(t, results.created, 3)
	for index, result := range results.created {
		require.Equal(t, int64(index+1), *result.TestDefinitionID)
		require.Equal(t, "account", result.TargetMode)
		if index == 1 {
			require.Equal(t, "failed", result.Status)
			require.Contains(t, result.ErrorMessage, "disabled")
		} else {
			require.Equal(t, "success", result.Status)
			require.Equal(t, accountID, *result.AccountID)
		}
	}
	require.Equal(t, []string{"running", "running", "running"}, results.createdStatuses)
	require.Nil(t, plan.TestDefinitionID, "execution copies must not rewrite the stored rule")
}

func TestScheduledTestRetryPreservesTypeAndTargetAfterRuleChanges(t *testing.T) {
	accountID, groupID, oldDefinitionID := int64(12), int64(7), int64(2)
	previous := &ScheduledTestResult{
		ID: 9, PlanID: 3, AccountID: &accountID, GroupID: &groupID,
		TestDefinitionID: &oldDefinitionID, TargetMode: "group", Status: "failed",
		ModelID: "original-model", ReasoningEffort: "high", CreatedAt: time.Now().Add(-time.Hour),
	}
	results := &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 1)}
	svc := NewScheduledTestService(nil, results)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "different check", OutputKind: "html"},
		2: {ID: 2, Enabled: true, Prompt: "original check", OutputKind: "text"},
	}})
	accounts := retryAccountRepoStub{account: &Account{ID: accountID, GroupIDs: []int64{groupID}, Extra: map[string]any{"synthetic_ui_test": true}}}
	runner := NewScheduledTestRunnerService(nil, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	plan := &ScheduledTestPlan{ID: 3, GroupID: scheduledTestPtrInt64(8), TargetMode: "all_accounts", TestDefinitionID: scheduledTestPtrInt64(1), TestDefinitionIDs: []int64{1}, ModelID: "new-model", ReasoningEffort: "low"}
	pending, err := runner.RetryAccount(context.Background(), plan, previous)
	require.NoError(t, err)
	require.Equal(t, previous.ID, pending.ID)
	require.Equal(t, oldDefinitionID, *pending.TestDefinitionID)
	require.Equal(t, "group", pending.TargetMode)
	require.Equal(t, groupID, *pending.GroupID)
	require.Equal(t, previous.ModelID, pending.ModelID)
	require.Equal(t, previous.ReasoningEffort, pending.ReasoningEffort)
	select {
	case completed := <-results.updated:
		require.Equal(t, previous.ID, completed.ID)
		require.Equal(t, oldDefinitionID, *completed.TestDefinitionID)
		require.Equal(t, "success", completed.Status)
		require.Equal(t, "text", completed.OutputKind)
	case <-time.After(2 * time.Second):
		t.Fatal("retry did not complete")
	}
	require.Empty(t, results.created, "retry updates its original row")
	require.Equal(t, []int64{1}, plan.TestDefinitionIDs)
}

func TestScheduledTestMultipleDefinitionsRunEveryAccountTypePair(t *testing.T) {
	plans := &runnerPlanRepoStub{}
	results := &retryResultRepoStub{updated: make(chan *ScheduledTestResult, 4)}
	svc := NewScheduledTestService(plans, results)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "first", OutputKind: "text"},
		2: {ID: 2, Enabled: true, Prompt: "second", OutputKind: "text"},
	}})
	accounts := multiDefinitionAccountRepoStub{accounts: []Account{
		{ID: 12, Extra: map[string]any{"synthetic_ui_test": true}},
		{ID: 13, Extra: map[string]any{"synthetic_ui_test": true}},
	}}
	runner := NewScheduledTestRunnerService(plans, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	runner.runOnePlan(context.Background(), &ScheduledTestPlan{ID: 7, GroupID: scheduledTestPtrInt64(8), TargetMode: "all_accounts", TestDefinitionIDs: []int64{1, 2}, ModelID: "model", CronExpression: "* * * * *", MaxResults: 10})
	require.Equal(t, 1, plans.updated)
	require.Len(t, results.created, 4)
	pairs := make(map[[2]int64]bool)
	for range 4 {
		result := <-results.updated
		require.Equal(t, "success", result.Status)
		require.Equal(t, "all_accounts", result.TargetMode)
		pairs[[2]int64{*result.AccountID, *result.TestDefinitionID}] = true
	}
	require.Len(t, pairs, 4)
	for _, account := range accounts.accounts {
		for _, definitionID := range []int64{1, 2} {
			require.True(t, pairs[[2]int64{account.ID, definitionID}])
		}
	}
}

func TestScheduledTestSkippedAccountStillProducesEveryConfiguredType(t *testing.T) {
	plans := &runnerPlanRepoStub{}
	results := &targetAccountResultRepo{runnerResultRepoStub: &runnerResultRepoStub{}}
	svc := NewScheduledTestService(plans, results)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		1: {ID: 1, Enabled: true, Prompt: "first", OutputKind: "text"},
		2: {ID: 2, Enabled: true, Prompt: "second", OutputKind: "text"},
		3: {ID: 3, Enabled: true, Prompt: "third", OutputKind: "text"},
	}})
	runner := NewScheduledTestRunnerService(plans, svc, nil, nil, nil, nil)
	accountID, groupID := int64(42), int64(8)
	runner.runOnePlan(context.Background(), &ScheduledTestPlan{
		ID: 18, AccountID: &accountID, GroupID: &groupID, TargetMode: "account",
		TestDefinitionIDs: []int64{1, 2, 3}, ModelID: "model", CronExpression: "* * * * *",
		Enabled: true, MaxResults: 10,
	})
	require.Len(t, results.created, 3)
	for index, result := range results.created {
		require.Equal(t, int64(index+1), *result.TestDefinitionID)
		require.Equal(t, "failed", result.Status)
		require.Contains(t, result.ErrorMessage, "检测跳过")
		require.Equal(t, accountID, *result.AccountID)
	}
}

func TestScheduledTestRetryDoesNotSkipAnotherDefinitionForSameAccount(t *testing.T) {
	accountID, firstID, secondID := int64(12), int64(1), int64(2)
	previous := &ScheduledTestResult{ID: 9, PlanID: 7, AccountID: &accountID, TestDefinitionID: &firstID, TargetMode: "account", ModelID: "model", Status: "failed"}
	results := &multiDefinitionObservedResultRepo{
		retryResultRepoStub: &retryResultRepoStub{previous: previous, updated: make(chan *ScheduledTestResult, 2)},
		started:             make(chan *ScheduledTestResult, 1),
	}
	svc := NewScheduledTestService(nil, results)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		firstID:  {ID: firstID, Enabled: true, Prompt: "first", OutputKind: "text"},
		secondID: {ID: secondID, Enabled: true, Prompt: "second", OutputKind: "text"},
	}})
	accounts := retryAccountRepoStub{account: &Account{ID: accountID, Extra: map[string]any{"synthetic_ui_test": true}}}
	runner := NewScheduledTestRunnerService(nil, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	// Hold the only worker so A's retry cannot finish before B is dispatched.
	runner.workerSem = make(chan struct{}, 1)
	runner.workerSem <- struct{}{}
	blocked := true
	defer func() {
		if blocked {
			<-runner.workerSem
		}
	}()
	first := &ScheduledTestPlan{ID: 7, AccountID: &accountID, TargetMode: "account", TestDefinitionID: &firstID, ModelID: "model"}
	_, err := runner.RetryAccount(context.Background(), first, previous)
	require.NoError(t, err)
	_, err = runner.RetryAccount(context.Background(), first, previous)
	require.ErrorIs(t, err, ErrScheduledTestAccountRunning, "the same type must still reject duplicate retries")
	runner.runOneAccount(context.Background(), first, accountID, "first", "text")
	require.Zero(t, results.countCreated(), "cron execution must not duplicate the same running check")

	second := *first
	second.TestDefinitionID = &secondID
	done := make(chan struct{})
	go func() {
		defer close(done)
		runner.runPlanDefinition(context.Background(), &second)
	}()
	select {
	case started := <-results.started:
		require.Equal(t, secondID, *started.TestDefinitionID)
		require.Equal(t, "running", started.Status)
	case <-time.After(2 * time.Second):
		t.Fatal("type B was skipped while type A's retry held the account lock")
	}
	<-runner.workerSem
	blocked = false
	completed := make(map[int64]*ScheduledTestResult)
	for range 2 {
		select {
		case result := <-results.updated:
			require.Equal(t, "success", result.Status)
			completed[*result.TestDefinitionID] = result
		case <-time.After(2 * time.Second):
			t.Fatal("both definitions must finish after the worker is released")
		}
	}
	<-done
	require.Equal(t, previous.ID, completed[firstID].ID, "A retries its original row")
	require.NotEqual(t, previous.ID, completed[secondID].ID, "B keeps its independent execution")
	require.Equal(t, 1, results.countCreated())
}
