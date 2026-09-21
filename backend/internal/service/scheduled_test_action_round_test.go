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

type actionRoundResultRepo struct {
	*protectionRepositoryStub
	beginRoundErr error
	roundCalls    int
	definitions   []int64
	roundStart    time.Time
	roundDeadline time.Time
	roundContext  context.Context
}

func (r *actionRoundResultRepo) BeginProtectionRun(ctx context.Context, plan *ScheduledTestPlan, started time.Time) error {
	r.record("round")
	r.roundCalls++
	r.definitions = append([]int64(nil), plan.TestDefinitionIDs...)
	r.roundStart = started
	r.roundDeadline, _ = ctx.Deadline()
	r.roundContext = ctx
	return r.beginRoundErr
}

type actionRoundDefinitionRepo struct {
	multiDefinitionRepoStub
	observe func(int64)
}

func (r actionRoundDefinitionRepo) GetByID(ctx context.Context, id int64) (*ScheduledTestDefinition, error) {
	r.observe(id)
	return r.multiDefinitionRepoStub.GetByID(ctx, id)
}

func actionRoundPlan() *ScheduledTestPlan {
	plan := protectionPlan()
	plan.TestDefinitionIDs = []int64{2, 1}
	plan.CronExpression = "* * * * *"
	plan.Protection.Rules = []ScheduledTestProtectionRule{
		{TestDefinitionID: 1, PauseOnFailure: true},
		{TestDefinitionID: 2, PauseOnFailure: true},
	}
	return plan
}

func TestScheduledTestActionRoundPrecedesEveryDefinition(t *testing.T) {
	base := &protectionRepositoryStub{eligible: true}
	repo := &actionRoundResultRepo{protectionRepositoryStub: base}
	base.collect = func(_ context.Context, _ ScheduledTestStatisticsFilter) (*ScheduledTestStatistics, error) {
		return &ScheduledTestStatistics{}, nil
	}
	var upstreamCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		upstreamCalls.Add(1)
		base.record("upstream")
		return &Account{ID: id, Status: "active", Schedulable: true, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	plans := &runnerPlanRepoStub{}
	svc := NewScheduledTestService(plans, repo)
	svc.SetDefinitionRepository(actionRoundDefinitionRepo{
		multiDefinitionRepoStub: multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
			1: {ID: 1, Enabled: true, OutputKind: "statistics"},
			2: {ID: 2, Enabled: true, OutputKind: "text", Prompt: "model check"},
		}},
		observe: func(id int64) { base.record(fmt.Sprintf("definition:%d", id)) },
	})
	runner := NewScheduledTestRunnerService(plans, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	plan := actionRoundPlan()
	before := time.Now()
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, 1, repo.roundCalls)
	require.Equal(t, []int64{2, 1}, repo.definitions, "initialization needs the complete plan before per-type splitting")
	require.False(t, repo.roundStart.Before(before))
	require.Equal(t, time.UTC, repo.roundStart.Location())
	require.Greater(t, repo.roundDeadline.Sub(repo.roundStart), time.Duration(0))
	require.LessOrEqual(t, repo.roundDeadline.Sub(repo.roundStart), scheduledTestPersistenceTimeout)
	require.ErrorIs(t, repo.roundContext.Err(), context.Canceled, "release the initialization timer immediately")
	require.Equal(t, "round", base.events[0], "neither local statistics nor upstream definition lookup may run first")
	require.Contains(t, base.events, "collect")
	require.Contains(t, base.events, "upstream")
	require.Equal(t, int32(1), upstreamCalls.Load())
	require.Equal(t, 1, plans.updated)

	base.events = nil
	runner.runOnePlan(context.Background(), plan)
	require.Equal(t, 2, repo.roundCalls, "each full execution must invalidate prior conclusions")
	require.Equal(t, "round", base.events[0])
}

func TestScheduledTestActionRoundInitializationFailureStopsExecution(t *testing.T) {
	base := &protectionRepositoryStub{eligible: true}
	repo := &actionRoundResultRepo{protectionRepositoryStub: base, beginRoundErr: errors.New("round state unavailable")}
	plans := &runnerPlanRepoStub{}
	svc := NewScheduledTestService(plans, repo)
	var definitionCalls atomic.Int32
	svc.SetDefinitionRepository(actionRoundDefinitionRepo{
		observe: func(int64) { definitionCalls.Add(1) },
	})
	var upstreamCalls atomic.Int32
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		upstreamCalls.Add(1)
		return nil, errors.New("must not call upstream")
	}}
	runner := NewScheduledTestRunnerService(plans, svc, &AccountTestService{accountRepo: accounts}, accounts, nil, nil)
	runner.runOnePlan(context.Background(), actionRoundPlan())
	require.Equal(t, 1, repo.roundCalls)
	require.Equal(t, []string{"round"}, base.events)
	require.Zero(t, definitionCalls.Load())
	require.Zero(t, upstreamCalls.Load())
	require.Nil(t, base.created)
	require.Equal(t, 1, plans.updated, "a failing storage dependency must not create an immediate cron retry loop")
}

func TestScheduledTestActionRoundOptionalCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name            string
		protection      bool
		roundCapability bool
	}{
		{name: "protection disabled does not prepare rounds", roundCapability: true},
		{name: "legacy protection adapter continues", protection: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			base := &protectionRepositoryStub{eligible: true}
			round := &actionRoundResultRepo{protectionRepositoryStub: base, beginRoundErr: errors.New("must not prepare")}
			var results ScheduledTestResultRepository = base
			if tc.roundCapability {
				results = round
			}
			var upstreamCalls atomic.Int32
			accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
				upstreamCalls.Add(1)
				return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
			}}
			runner := newScheduledTestExecutionRunner(results, accounts)
			plan := protectionPlan()
			plan.Protection.Enabled = tc.protection
			plan.CronExpression = "* * * * *"
			runner.runOnePlan(context.Background(), plan)
			require.Zero(t, round.roundCalls)
			require.Equal(t, int32(1), upstreamCalls.Load())
			require.Equal(t, "success", base.completed.Status)
		})
	}
}

func TestScheduledTestActionRoundSingleDefinitionExecutionDoesNotResetOthers(t *testing.T) {
	base := &protectionRepositoryStub{eligible: true}
	repo := &actionRoundResultRepo{protectionRepositoryStub: base, beginRoundErr: errors.New("must not reset the full round")}
	accounts := scheduledTestExecutionAccountRepo{get: func(_ context.Context, id int64) (*Account, error) {
		return &Account{ID: id, Extra: map[string]any{"synthetic_ui_test": true}}, nil
	}}
	runner := newScheduledTestExecutionRunner(repo, accounts)
	runner.runOneAccount(context.Background(), protectionPlan(), 42, "retry this type", "text")
	require.Zero(t, repo.roundCalls)
	require.Contains(t, base.events, "begin", "individual execution still fences its own result generation")
	require.Equal(t, "success", base.completed.Status)
}
