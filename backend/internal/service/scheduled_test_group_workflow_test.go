package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func groupWorkflowPlan() *ScheduledTestPlan {
	return &ScheduledTestPlan{
		ID: 7, Enabled: true, TargetMode: "all_accounts", GroupID: scheduledTestPtrInt64(2),
		TestDefinitionIDs: []int64{12, 11}, ModelID: "gpt-6-astra", ReasoningEffort: "ultra", CronExpression: "0 * * * *", MaxResults: 50,
		Protection: ScheduledTestProtectionConfig{Enabled: true, GroupWorkflow: &ScheduledTestGroupWorkflow{AutomaticTestID: 11, ReviewTestID: 12, PassGroupID: 43, FailGroupID: 2}},
	}
}

func TestScheduledTestGroupWorkflowNormalizesHiddenLegacyActions(t *testing.T) {
	plan := groupWorkflowPlan()
	plan.AutoRecover = true
	plan.Protection.Rules = []ScheduledTestProtectionRule{{TestDefinitionID: 11, ExpectedAnswer: "29", OnFail: &ScheduledTestOutcomeAction{Scheduling: "pause"}, Recovery: &ScheduledTestCacheRecoveryConfig{Enabled: true}}}
	require.NoError(t, validateScheduledTestPlan(plan))
	require.Equal(t, []int64{11, 12}, plan.TestDefinitionIDs)
	require.Equal(t, int64(11), *plan.TestDefinitionID)
	require.False(t, plan.AutoRecover)
	require.Len(t, plan.Protection.Rules, 2)
	rule := plan.Protection.Rules[0]
	require.Equal(t, "21", rule.ExpectedAnswer)
	require.Equal(t, "numeric", rule.AnswerMatch)
	require.Nil(t, rule.Vote)
	require.Nil(t, rule.Recovery)
	for _, rule := range plan.Protection.Rules {
		require.Equal(t, "keep", rule.OutcomeAction("fail").Scheduling)
		require.Equal(t, "keep", rule.OutcomeAction("pass").Scheduling)
		require.Equal(t, []int64{43}, rule.OutcomeAction("pass").GroupIDs)
		require.Equal(t, []int64{2}, rule.OutcomeAction("fail").GroupIDs)
	}
	for _, tc := range []struct {
		value float64
		want  string
	}{{21, "pass"}, {21.0, "pass"}, {20, "fail"}, {29, "fail"}} {
		verdict, _ := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputNumeric: protectionFloat(tc.value)})
		require.Equal(t, tc.want, verdict)
	}
	verdict, _ := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "failed"})
	require.Equal(t, "fail", verdict)
	encoded, err := json.Marshal(plan.Protection)
	require.NoError(t, err)
	var decoded ScheduledTestProtectionConfig
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.Equal(t, plan.Protection.GroupWorkflow, decoded.GroupWorkflow)
}

func TestScheduledTestGroupWorkflowValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ScheduledTestPlan)
	}{
		{"same groups", func(p *ScheduledTestPlan) { p.Protection.GroupWorkflow.PassGroupID = 2 }},
		{"missing group", func(p *ScheduledTestPlan) { p.Protection.GroupWorkflow.FailGroupID = 0 }},
		{"same tests", func(p *ScheduledTestPlan) { p.Protection.GroupWorkflow.ReviewTestID = 11 }},
		{"missing test", func(p *ScheduledTestPlan) { p.Protection.GroupWorkflow.AutomaticTestID = 0 }},
		{"unrelated anchor", func(p *ScheduledTestPlan) { p.GroupID = scheduledTestPtrInt64(9) }},
		{"single account", func(p *ScheduledTestPlan) { p.TargetMode = "account"; p.AccountID = scheduledTestPtrInt64(1) }},
		{"aggregate group", func(p *ScheduledTestPlan) { p.TargetMode = "group" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := groupWorkflowPlan()
			tc.mutate(p)
			require.Error(t, validateScheduledTestPlan(p))
		})
	}
	for _, tc := range []struct {
		name, automaticKind, reviewKind string
		valid                           bool
	}{
		{"valid", "number", "html", true},
		{"text auto", "text", "html", false},
		{"numeric review", "number", "number", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := groupWorkflowPlan()
			require.NoError(t, validateScheduledTestPlan(p))
			svc := NewScheduledTestService(nil, nil)
			svc.SetDefinitionRepository(multiDefinitionRepoStub{definitions: map[int64]*ScheduledTestDefinition{11: {ID: 11, Enabled: true, OutputKind: tc.automaticKind}, 12: {ID: 12, Enabled: true, OutputKind: tc.reviewKind}}})
			err := svc.validateDefinitionForPlan(context.Background(), p)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.Error(t, err)
			}
		})
	}
}

func TestScheduledTestGroupWorkflowAlwaysExecutesAutomaticBeforeReview(t *testing.T) {
	p := groupWorkflowPlan()
	// Stored selection order or a legacy client must not make a review run
	// before the automatic decision that establishes this round's baseline.
	executions := scheduledTestExecutionPlans(p)
	require.Len(t, executions, 2)
	require.Equal(t, int64(11), *executions[0].TestDefinitionID)
	require.Equal(t, int64(12), *executions[1].TestDefinitionID)
	require.Equal(t, []int64{12, 11}, p.TestDefinitionIDs, "runner must not mutate a shared plan snapshot")
}

func TestScheduledTestGroupWorkflowPublicVotingIsExplicit(t *testing.T) {
	p := groupWorkflowPlan()
	require.NoError(t, validateScheduledTestPlan(p))
	require.True(t, p.Protection.Rules[1].Vote.Enabled)
	require.False(t, p.Protection.Rules[1].Vote.PublicEnabled)
	p.Protection.GroupWorkflow.ReviewVote = &ScheduledTestVoteConfig{PublicEnabled: true, PassAtLeast: 3, RejectAbove: 1}
	require.NoError(t, validateScheduledTestPlan(p))
	require.True(t, p.Protection.Rules[1].Vote.Enabled, "workflow administrator review stays enabled")
	require.True(t, p.Protection.Rules[1].Vote.PublicEnabled)
	require.Equal(t, 3, p.Protection.Rules[1].Vote.PassAtLeast)
	require.Nil(t, p.Protection.Rules[0].Vote)
	p.Protection.GroupWorkflow.ReviewVote.PassAtLeast = 0
	require.Error(t, validateScheduledTestPlan(p))
}
