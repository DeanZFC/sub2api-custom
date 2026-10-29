package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProtectionDetailsRetainsHitButNeverReusesPreviousRoundEvidence(t *testing.T) {
	now := time.Now()
	source := ScheduledTestProtectionHoldSource{PlanID: 1, PlanName: "quality", Reason: "命中组合规则：不通过", UpdatedAt: now,
		RuleIDs: []string{"failed"}, Config: ScheduledTestProtectionConfig{Combinations: []ScheduledTestCombinationRule{{
			ID: "failed", Name: "不通过", Condition: ScheduledTestCombinationCondition{Operator: "any", Conditions: []ScheduledTestCombinationCondition{
				{Operator: "test", TestDefinitionID: 2, Verdict: "fail"}, {Operator: "test", TestDefinitionID: 3, Verdict: "fail"},
			}},
		}}}, States: []ScheduledTestProtectionStateSource{
			{DefinitionID: 2, TestName: "缓存", Current: false, RoundStarted: now.Add(time.Minute), Verdict: "fail", Automated: "fail", Reason: "old reason", FailCount: 10,
				Rule: ScheduledTestProtectionRule{Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "gt", Value: 100}}}, Result: &ScheduledTestResult{ID: 4, Status: "success", LatencyMs: 500}},
			{DefinitionID: 3, TestName: "审核", Current: true, RoundStarted: now.Add(time.Minute), Verdict: "pass", Automated: "pass", AdminVerdict: "pass", Result: &ScheduledTestResult{ID: 5, Status: "success"}},
			{DefinitionID: 9, TestName: "unrelated", Current: true, Verdict: "fail"},
		}}
	hold := buildScheduledTestProtectionHold(source)
	require.True(t, hold.PriorRound)
	require.Equal(t, "不通过", hold.Rules[0].Name)
	require.Equal(t, "pending", hold.Rules[0].CurrentMatch)
	require.Len(t, hold.Tests, 2)
	require.Equal(t, "pending", hold.Tests[0].Verdict)
	require.Empty(t, hold.Tests[0].Reason)
	require.Zero(t, hold.Tests[0].FailCount)
	require.Nil(t, hold.Tests[0].Thresholds[0].Observed)
	require.Equal(t, "pass", hold.Tests[1].AdminVerdict)

	// A valid new pass does not erase the persisted pause or label it a new hit.
	source.States[0].Current, source.States[0].Verdict = true, "pass"
	hold = buildScheduledTestProtectionHold(source)
	require.True(t, hold.PriorRound)
	require.Equal(t, "fail", hold.Rules[0].CurrentMatch)
	require.Equal(t, source.Reason, hold.Reason)
}

func TestProtectionDetailsMissingRuleDoesNotInventConditions(t *testing.T) {
	hold := buildScheduledTestProtectionHold(ScheduledTestProtectionHoldSource{RuleIDs: []string{"removed-rule"}})
	require.Len(t, hold.Rules, 1)
	require.Equal(t, "removed-rule", hold.Rules[0].Name)
	require.Nil(t, hold.Rules[0].Condition)
	require.Equal(t, "pending", hold.Rules[0].CurrentMatch)
	require.Empty(t, hold.Tests)
}

func TestProtectionDetailsFailedExecutionHasNoSyntheticMetrics(t *testing.T) {
	hold := buildScheduledTestProtectionHold(ScheduledTestProtectionHoldSource{DefinitionID: 2, States: []ScheduledTestProtectionStateSource{{
		DefinitionID: 2, Current: true, Automated: "fail", Verdict: "fail",
		Rule:   ScheduledTestProtectionRule{PauseOnFailure: true, Thresholds: []ScheduledTestThreshold{{Metric: "latency_ms", Operator: "lt", Value: 100}}},
		Result: &ScheduledTestResult{Status: "failed"},
	}}})
	require.Equal(t, "检测执行失败", hold.Tests[0].AutomaticReason)
	require.Nil(t, hold.Tests[0].Thresholds[0].Observed)
}
