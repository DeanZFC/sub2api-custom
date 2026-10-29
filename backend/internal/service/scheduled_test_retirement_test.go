package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

type retiredStatisticsDefinitionRepoStub struct {
	ScheduledTestDefinitionRepository
	definitions []*ScheduledTestDefinition
}

func (r retiredStatisticsDefinitionRepoStub) List(context.Context, bool) ([]*ScheduledTestDefinition, error) {
	return r.definitions, nil
}

func TestScheduledTestRetiredStatisticsCannotBeCreatedOrUpdated(t *testing.T) {
	service := NewScheduledTestService(nil, nil)
	service.SetDefinitionRepository(retiredStatisticsDefinitionRepoStub{})
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			definition := &ScheduledTestDefinition{ID: 3, Key: "legacy-statistics", Name: "Legacy statistics", Prompt: "A prompt cannot reactivate statistics", OutputKind: " STATISTICS "}
			write := service.CreateDefinition
			if operation == "update" {
				write = service.UpdateDefinition
			}
			_, err := write(context.Background(), definition)
			require.ErrorContains(t, err, "statistics tests have been retired")
		})
	}
}

func TestScheduledTestRetiredStatisticsPlansCannotBeCreatedOrUpdated(t *testing.T) {
	for _, operation := range []string{"create", "update"} {
		t.Run(operation, func(t *testing.T) {
			plans := &multiDefinitionPlanRepoStub{}
			service := NewScheduledTestService(plans, nil)
			service.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
				3: {ID: 3, Enabled: true, Prompt: "legacy prompt", OutputKind: " STATISTICS "},
			}})
			plan := &ScheduledTestPlan{ID: 7, GroupIDs: []int64{1}, TestDefinitionIDs: []int64{3}, ModelID: "model", CronExpression: "* * * * *"}
			write := service.CreatePlan
			if operation == "update" {
				write = service.UpdatePlan
			}
			_, err := write(context.Background(), plan)
			require.ErrorContains(t, err, "statistics tests have been retired")
			require.Nil(t, plans.saved, "disabled protection must not bypass the retired definition check")
		})
	}
}

func TestScheduledTestRetiredStatisticsAreHiddenFromDefinitionLists(t *testing.T) {
	active := &ScheduledTestDefinition{ID: 1, OutputKind: "capability"}
	service := NewScheduledTestService(nil, nil)
	service.SetDefinitionRepository(retiredStatisticsDefinitionRepoStub{definitions: []*ScheduledTestDefinition{
		{ID: 2, OutputKind: " STATISTICS "}, nil, active,
	}})
	for _, enabledOnly := range []bool{false, true} {
		definitions, err := service.ListDefinitions(context.Background(), enabledOnly)
		require.NoError(t, err)
		require.Equal(t, []*ScheduledTestDefinition{active}, definitions)
	}
}

func TestScheduledTestRetiredStatisticsCannotExecuteLegacyPlans(t *testing.T) {
	service := NewScheduledTestService(nil, nil)
	service.SetDefinitionRepository(scheduledTestDefinitionRepoStub{definition: &ScheduledTestDefinition{
		ID: 3, Enabled: true, Prompt: "legacy prompt", OutputKind: " STATISTICS ",
	}})
	runner := &ScheduledTestRunnerService{scheduledSvc: service}
	prompt, kind, tokens, err := runner.resolveDefinition(context.Background(), &ScheduledTestPlan{TestDefinitionID: scheduledTestPtrInt64(3)})
	require.ErrorContains(t, err, "statistics tests have been retired")
	require.Empty(t, prompt)
	require.Empty(t, kind)
	require.Zero(t, tokens)
}
