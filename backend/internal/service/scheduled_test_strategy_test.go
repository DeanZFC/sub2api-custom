package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func strategyPlan() *ScheduledTestPlan {
	return &ScheduledTestPlan{
		ID: 7, Enabled: true, GroupIDs: []int64{43, 2},
		TestDefinitionIDs: []int64{11, 12, 13}, ModelID: "gpt-6-astra", ReasoningEffort: "ultra", CronExpression: "0 * * * *", MaxResults: 50,
		Protection: ScheduledTestProtectionConfig{Enabled: true, Rules: []ScheduledTestProtectionRule{
			{TestDefinitionID: 11, ExpectedAnswer: "21", AnswerMatch: "numeric", OnPass: &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{43}}, OnFail: &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{2}}},
			{TestDefinitionID: 12, Priority: 100, Vote: &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}, OnPass: &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{43}}, OnFail: &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{2}}},
			{TestDefinitionID: 13, ModelMatch: "exact", RequiredPass: true},
		}},
	}
}

func TestScheduledTestStrategyArbitraryChecksAndOrderedGroups(t *testing.T) {
	plan := strategyPlan()
	plan.GroupIDs = []int64{43, 2, 43}
	require.NoError(t, validateScheduledTestPlan(plan))
	require.Equal(t, []int64{43, 2}, plan.GroupIDs)
	require.EqualValues(t, 43, *plan.GroupID)
	require.Nil(t, plan.AccountID)
	require.Equal(t, "all_accounts", plan.TargetMode)
	require.Equal(t, []int64{11, 12, 13}, plan.TestDefinitionIDs)
	svc := NewScheduledTestService(nil, nil)
	svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{
		11: {ID: 11, Enabled: true, OutputKind: "number"}, 12: {ID: 12, Enabled: true, OutputKind: "html"}, 13: {ID: 13, Enabled: true, OutputKind: "model_check"},
	}})
	require.NoError(t, svc.validateDefinitionForPlan(context.Background(), plan))
	require.Equal(t, "21", plan.Protection.Rules[0].ExpectedAnswer)
	plan.Protection.Rules[0].ExpectedAnswer = "29"
	require.NoError(t, validateScheduledTestPlan(plan))
	require.Equal(t, "29", plan.Protection.Rules[0].ExpectedAnswer, "no built-in answer may override configuration")
	plan.TestDefinitionIDs = []int64{13, 12, 11}
	executions := scheduledTestExecutionPlans(plan)
	for i, id := range plan.TestDefinitionIDs {
		require.Equal(t, id, *executions[i].TestDefinitionID)
	}
}

func TestScheduledTestStrategyRejectsInvalidRouting(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ScheduledTestPlan)
	}{
		{"missing groups", func(p *ScheduledTestPlan) { p.GroupIDs = nil }},
		{"invalid group", func(p *ScheduledTestPlan) { p.GroupIDs = []int64{0} }},
		{"missing tests", func(p *ScheduledTestPlan) { p.TestDefinitionIDs = nil }},
		{"negative priority", func(p *ScheduledTestPlan) { p.Protection.Rules[0].Priority = -1 }},
		{"excess priority", func(p *ScheduledTestPlan) { p.Protection.Rules[0].Priority = 1001 }},
		{"review hard condition", func(p *ScheduledTestPlan) { p.Protection.Rules[1].RequiredPass = true }},
		{"destination outside next round", func(p *ScheduledTestPlan) { p.Protection.Rules[0].OnPass.GroupIDs = []int64{9} }},
		{"empty destination", func(p *ScheduledTestPlan) { p.Protection.Rules[0].OnPass.GroupIDs = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) { p := strategyPlan(); tc.mutate(p); require.Error(t, validateScheduledTestPlan(p)) })
	}
}
