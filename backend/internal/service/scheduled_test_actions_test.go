package service

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestActionsLegacyAndExplicitNoop(t *testing.T) {
	var legacy ScheduledTestProtectionRule
	require.NoError(t, json.Unmarshal([]byte(`{"test_definition_id":1,"pause_on_failure":true}`), &legacy))
	require.Equal(t, ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}, legacy.OutcomeAction("pass"))
	require.Equal(t, ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}, legacy.OutcomeAction("fail"))
	for _, verdict := range []string{"pending", "", "unknown"} {
		require.Equal(t, ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}, legacy.OutcomeAction(verdict))
	}

	// Explicit no-ops must not fall back to the legacy pause/recover policy.
	legacy.OnPass = &ScheduledTestOutcomeAction{}
	legacy.OnFail = &ScheduledTestOutcomeAction{}
	for _, verdict := range []string{"pass", "fail"} {
		require.Equal(t, ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}, legacy.OutcomeAction(verdict))
	}
	encoded, err := json.Marshal(legacy)
	require.NoError(t, err)
	var decoded ScheduledTestProtectionRule
	require.NoError(t, json.Unmarshal(encoded, &decoded))
	require.NotNil(t, decoded.OnPass)
	require.NotNil(t, decoded.OnFail)
	require.Equal(t, "keep", decoded.OutcomeAction("fail").Scheduling)
}

func TestScheduledTestActionsManagedScopeAndIsolation(t *testing.T) {
	rule := ScheduledTestProtectionRule{
		OnPass: &ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{30, 20, 30}},
		OnFail: &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{20, 10}},
	}
	require.Equal(t, []int64{10, 20, 30}, rule.ManagedGroupIDs())
	require.Equal(t, []int64{30, 20, 30}, rule.OnPass.GroupIDs, "scope lookup must not mutate configuration")
	action := rule.OutcomeAction("pass")
	require.Equal(t, "resume", action.Scheduling)
	require.Equal(t, "assign", action.GroupMode)
	action.GroupIDs[0] = 999
	require.Equal(t, int64(30), rule.OnPass.GroupIDs[0], "resolved actions must not alias the configured IDs")
	scope := rule.ManagedGroupIDs()
	scope[0] = 999
	require.Equal(t, int64(20), rule.OnFail.GroupIDs[0])

	// Empty assign means remove this rule's groups for that outcome. It keeps
	// the other outcome's IDs in scope without including unrelated bindings.
	rule.OnFail.GroupIDs = nil
	require.NoError(t, validateScheduledTestActions(&rule))
	require.Equal(t, []int64{20, 30}, rule.ManagedGroupIDs())
	require.Empty(t, rule.OutcomeAction("fail").GroupIDs)
	require.Equal(t, "assign", rule.OutcomeAction("fail").GroupMode)
}

func TestScheduledTestActionsValidation(t *testing.T) {
	for _, tc := range []struct {
		name string
		pass *ScheduledTestOutcomeAction
		fail *ScheduledTestOutcomeAction
	}{
		{name: "invalid scheduling", pass: &ScheduledTestOutcomeAction{Scheduling: "delete"}},
		{name: "invalid fail scheduling", fail: &ScheduledTestOutcomeAction{Scheduling: "active"}},
		{name: "invalid group mode", pass: &ScheduledTestOutcomeAction{GroupMode: "replace_all"}},
		{name: "keep cannot hide groups", pass: &ScheduledTestOutcomeAction{GroupMode: "keep", GroupIDs: []int64{1}}},
		{name: "omitted mode cannot hide groups", fail: &ScheduledTestOutcomeAction{GroupIDs: []int64{1}}},
		{name: "negative group", pass: &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{-1}}},
		{name: "zero group", fail: &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{0}}},
		{name: "empty scope with legacy fail", pass: &ScheduledTestOutcomeAction{GroupMode: "assign"}},
		{name: "empty scope with keep pass", pass: &ScheduledTestOutcomeAction{GroupMode: "keep"}, fail: &ScheduledTestOutcomeAction{GroupMode: "assign"}},
		{name: "empty scope with two assignments", pass: &ScheduledTestOutcomeAction{GroupMode: "assign"}, fail: &ScheduledTestOutcomeAction{GroupMode: "assign"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := protectionPlan()
			plan.Protection.Rules[0].OnPass = tc.pass
			plan.Protection.Rules[0].OnFail = tc.fail
			require.Error(t, validateScheduledTestProtection(plan))
		})
	}

	plan := protectionPlan()
	plan.Protection.Rules[0].OnPass = &ScheduledTestOutcomeAction{}
	plan.Protection.Rules[0].OnFail = &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{9, 4, 9, 4}}
	require.NoError(t, validateScheduledTestProtection(plan))
	require.Equal(t, "keep", plan.Protection.Rules[0].OnPass.Scheduling)
	require.Equal(t, "keep", plan.Protection.Rules[0].OnPass.GroupMode)
	require.Equal(t, "keep", plan.Protection.Rules[0].OnFail.Scheduling)
	require.Equal(t, []int64{4, 9}, plan.Protection.Rules[0].OnFail.GroupIDs)

	// Each outcome may select 100 unique groups, including overlapping groups.
	ids := make([]int64, 100)
	for i := range ids {
		ids[i] = int64(i + 1)
	}
	plan.Protection.Rules[0].OnPass = &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: append(ids, 1)}
	require.NoError(t, validateScheduledTestProtection(plan))
	require.Len(t, plan.Protection.Rules[0].OnPass.GroupIDs, 100)
	plan.Protection.Rules[0].OnPass.GroupIDs = append(plan.Protection.Rules[0].OnPass.GroupIDs, 101)
	require.ErrorContains(t, validateScheduledTestProtection(plan), "at most 100")
}

func TestScheduledTestActionsPreserveVerdictGates(t *testing.T) {
	rule := ScheduledTestProtectionRule{
		Thresholds: []ScheduledTestThreshold{{Metric: "cache_rate", Operator: "lt", Value: 80}},
		MinSamples: 10,
		OnPass:     &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{2, 3}},
		OnFail:     &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{4}},
	}
	for _, tc := range []struct {
		name      string
		samples   int64
		cacheRate float64
		verdict   string
		groupIDs  []int64
	}{
		{name: "qualifying cache promotes", samples: 10, cacheRate: .8, verdict: "pass", groupIDs: []int64{2, 3}},
		{name: "low cache demotes", samples: 10, cacheRate: .79, verdict: "fail", groupIDs: []int64{4}},
		{name: "insufficient data cannot move", samples: 9, cacheRate: .9, verdict: "pending"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			verdict, _ := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputStatistics: &ScheduledTestStatistics{
				TotalRequests: tc.samples, CacheInputTokens: 100, CacheRate: protectionFloat(tc.cacheRate),
			}})
			require.Equal(t, tc.verdict, verdict)
			action := rule.OutcomeAction(verdict)
			require.Equal(t, tc.groupIDs, action.GroupIDs)
			require.Equal(t, "keep", action.Scheduling)
			if tc.verdict == "pending" {
				require.Equal(t, "keep", action.GroupMode)
			}
		})
	}

	// Numeric answers and voting share the same action mapping. Enabling voting
	// keeps a visual reference answer out of the automatic equality gate.
	rule.Thresholds = nil
	rule.ExpectedAnswer = "29"
	rule.AnswerMatch = "numeric"
	verdict, _ := evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", OutputNumeric: protectionFloat(29)})
	require.Equal(t, "pass", verdict)
	require.Equal(t, []int64{2, 3}, rule.OutcomeAction(verdict).GroupIDs)
	rule.Vote = &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 3, RejectAbove: 1}
	rule.ExpectedAnswer = "a correctly drawn pelican"
	verdict, _ = evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "success", ResponseText: "<svg>...</svg>"})
	require.Equal(t, "pass", verdict, "the repository must still wait for votes before resolving the final verdict")
	verdict, _ = evaluateScheduledTestProtection(rule, &ScheduledTestResult{Status: "failed"})
	require.Equal(t, "pending", verdict)
	require.Equal(t, "keep", rule.OutcomeAction(verdict).GroupMode)
}

func TestScheduledTestPlanHasGroupActions(t *testing.T) {
	var missing *ScheduledTestPlan
	require.False(t, missing.HasGroupActions())
	plan := protectionPlan()
	require.False(t, plan.HasGroupActions())
	plan.Protection.Rules[0].OnPass = &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{1}}
	require.True(t, plan.HasGroupActions())
	plan.Protection.Enabled = false
	require.False(t, plan.HasGroupActions())
}
