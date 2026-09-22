package service

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func combinationTestLeaf(id int64, verdict string) ScheduledTestCombinationCondition {
	return ScheduledTestCombinationCondition{Operator: "test", TestDefinitionID: id, Verdict: verdict}
}

func combinationTestGroup(operator string, children ...ScheduledTestCombinationCondition) ScheduledTestCombinationCondition {
	return ScheduledTestCombinationCondition{Operator: operator, Conditions: children}
}

func combinationTestPlan() *ScheduledTestPlan {
	plan := protectionPlan()
	plan.GroupIDs = []int64{10, 20, 30}
	plan.TestDefinitionIDs = []int64{1, 2, 3, 4}
	plan.Protection.Mode = "combined"
	plan.Protection.Rules = []ScheduledTestProtectionRule{
		{TestDefinitionID: 1, PauseOnFailure: true},
		{TestDefinitionID: 2, PauseOnFailure: true},
		{TestDefinitionID: 3, PauseOnFailure: true},
	}
	plan.Protection.Combinations = []ScheduledTestCombinationRule{{
		ID: "both_pass", Name: "Both checks pass", Priority: 1,
		Condition: combinationTestGroup("all", combinationTestLeaf(1, "pass"), combinationTestLeaf(2, "pass")),
		Action:    ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{10}},
	}}
	return plan
}

func TestScheduledTestCombinationConditionTruthTable(t *testing.T) {
	for _, tc := range []struct {
		first, second, all, any string
	}{
		{"pass", "pass", "pass", "pass"},
		{"pass", "fail", "fail", "pass"},
		{"pass", "pending", "pending", "pass"},
		{"fail", "pass", "fail", "pass"},
		{"fail", "fail", "fail", "fail"},
		{"fail", "pending", "fail", "pending"},
		{"pending", "pass", "pending", "pass"},
		{"pending", "fail", "fail", "pending"},
		{"pending", "pending", "pending", "pending"},
	} {
		t.Run(tc.first+"_"+tc.second, func(t *testing.T) {
			verdicts := map[int64]string{1: tc.first, 2: tc.second}
			all := combinationTestGroup("all", combinationTestLeaf(1, "pass"), combinationTestLeaf(2, "pass"))
			any := combinationTestGroup("any", combinationTestLeaf(1, "pass"), combinationTestLeaf(2, "pass"))
			require.Equal(t, tc.all, EvaluateScheduledTestCombinationCondition(all, verdicts))
			require.Equal(t, tc.any, EvaluateScheduledTestCombinationCondition(any, verdicts))
		})
	}
	for _, expected := range []string{"pass", "fail"} {
		condition := combinationTestLeaf(1, expected)
		for _, actual := range []string{"pending", "unknown", "running", ""} {
			require.Equal(t, "pending", EvaluateScheduledTestCombinationCondition(condition, map[int64]string{1: actual}))
		}
		require.Equal(t, "pending", EvaluateScheduledTestCombinationCondition(condition, nil))
		require.Equal(t, "pass", EvaluateScheduledTestCombinationCondition(condition, map[int64]string{1: expected}))
	}
	require.Equal(t, "fail", EvaluateScheduledTestCombinationCondition(combinationTestLeaf(1, "fail"), map[int64]string{1: "pass"}))
}

func TestScheduledTestCombinationsThreeValuedConditions(t *testing.T) {
	for _, tc := range []struct {
		name     string
		operator string
		first    string
		second   string
		matched  bool
	}{
		{"all checks pass", "all", "pass", "pass", true},
		{"all rejects a failed check", "all", "pass", "fail", false},
		{"all waits for pending", "all", "pass", "pending", false},
		{"all waits for absent", "all", "pass", "", false},
		{"all waits for unknown", "all", "pass", "unknown", false},
		{"all fails with failure and pending", "all", "fail", "pending", false},
		{"any passes with one success", "any", "pass", "fail", true},
		{"any passes despite pending", "any", "pending", "pass", true},
		{"any passes despite absent", "any", "", "pass", true},
		{"any passes despite unknown", "any", "unknown", "pass", true},
		{"any rejects both failures", "any", "fail", "fail", false},
		{"any waits with failure and pending", "any", "fail", "pending", false},
		{"any waits for all pending", "any", "pending", "pending", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := combinationTestPlan()
			plan.Protection.Combinations[0].Condition.Operator = tc.operator
			verdicts := map[int64]string{1: tc.first}
			if tc.second != "" {
				verdicts[2] = tc.second
			}
			decision := EvaluateScheduledTestCombinations(plan.Protection, verdicts)
			if tc.matched {
				require.NotNil(t, decision.Action)
				require.Equal(t, []int64{10}, decision.Action.GroupIDs)
				require.Equal(t, []string{"both_pass"}, decision.RuleIDs)
			} else {
				require.Nil(t, decision.Action)
				require.Empty(t, decision.RuleIDs)
			}
		})
	}
}

func TestScheduledTestCombinationsExplicitFailureIsNotAnElseBranch(t *testing.T) {
	plan := combinationTestPlan()
	plan.Protection.Combinations = append(plan.Protection.Combinations, ScheduledTestCombinationRule{
		ID: "either_fail", Priority: 1,
		Condition: combinationTestGroup("any", combinationTestLeaf(1, "fail"), combinationTestLeaf(2, "fail")),
		Action:    ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{20}},
	})
	for _, tc := range []struct {
		name     string
		verdicts map[int64]string
		groupIDs []int64
		ruleID   string
	}{
		{"both pass promotes", map[int64]string{1: "pass", 2: "pass"}, []int64{10}, "both_pass"},
		{"one failure demotes", map[int64]string{1: "fail", 2: "pass"}, []int64{20}, "either_fail"},
		{"failure can demote while another check is pending", map[int64]string{1: "pending", 2: "fail"}, []int64{20}, "either_fail"},
		{"both fail demotes", map[int64]string{1: "fail", 2: "fail"}, []int64{20}, "either_fail"},
		{"pending is neither pass nor fail", map[int64]string{1: "pass", 2: "pending"}, nil, ""},
		{"missing is not a failure", map[int64]string{1: "pass"}, nil, ""},
		{"unknown is not a failure", map[int64]string{1: "unknown", 2: "unknown"}, nil, ""},
		{"empty observations leave unchanged", nil, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := EvaluateScheduledTestCombinations(plan.Protection, tc.verdicts)
			if tc.groupIDs == nil {
				require.Nil(t, decision.Action)
				require.Empty(t, decision.RuleIDs)
				return
			}
			require.NotNil(t, decision.Action)
			require.Equal(t, tc.groupIDs, decision.Action.GroupIDs)
			require.Equal(t, []string{tc.ruleID}, decision.RuleIDs)
		})
	}
}

func TestScheduledTestCombinationsNestedConditions(t *testing.T) {
	plan := combinationTestPlan()
	// (Model passes AND Candy passes) OR (Pelican passes AND Model fails).
	plan.Protection.Combinations[0].Condition = combinationTestGroup("any",
		combinationTestGroup("all", combinationTestLeaf(1, "pass"), combinationTestLeaf(2, "pass")),
		combinationTestGroup("all", combinationTestLeaf(3, "pass"), combinationTestLeaf(1, "fail")),
	)
	for _, tc := range []struct {
		name     string
		verdicts map[int64]string
		matched  bool
	}{
		{"first branch is sufficient", map[int64]string{1: "pass", 2: "pass", 3: "pending"}, true},
		{"second branch is sufficient", map[int64]string{1: "fail", 2: "pending", 3: "pass"}, true},
		{"partial first branch does not pass", map[int64]string{1: "pass", 2: "pending", 3: "pass"}, false},
		{"partial second branch does not pass", map[int64]string{1: "pending", 2: "pass", 3: "pass"}, false},
		{"both branches false", map[int64]string{1: "pass", 2: "fail", 3: "fail"}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			decision := EvaluateScheduledTestCombinations(plan.Protection, tc.verdicts)
			require.Equal(t, tc.matched, decision.Action != nil)
		})
	}
}

func TestScheduledTestCombinationsOnlyMatchingPrioritiesCompete(t *testing.T) {
	plan := combinationTestPlan()
	plan.Protection.Combinations = append(plan.Protection.Combinations, ScheduledTestCombinationRule{
		ID: "pelican_reject", Priority: 2, Condition: combinationTestLeaf(3, "fail"),
		Action: ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{20}},
	})
	// Legacy source priorities and required-pass gates are not additional
	// constraints on the explicit expression in combination mode.
	plan.Protection.Rules[2].Priority = 1000
	plan.Protection.Rules[2].RequiredPass = true
	for _, tc := range []struct {
		verdict    string
		groupIDs   []int64
		scheduling string
		ruleID     string
	}{
		{"pending", []int64{10}, "keep", "both_pass"},
		{"pass", []int64{10}, "keep", "both_pass"},
		{"fail", []int64{20}, "pause", "pelican_reject"},
	} {
		t.Run(tc.verdict, func(t *testing.T) {
			decision := EvaluateScheduledTestCombinations(plan.Protection, map[int64]string{1: "pass", 2: "pass", 3: tc.verdict})
			require.NotNil(t, decision.Action)
			require.Equal(t, tc.groupIDs, decision.Action.GroupIDs)
			require.Equal(t, tc.scheduling, decision.Action.Scheduling)
			require.Equal(t, []string{tc.ruleID}, decision.RuleIDs)
		})
	}
}

func TestScheduledTestCombinationsMergeSamePriorityActions(t *testing.T) {
	plan := combinationTestPlan()
	plan.Protection.Combinations = []ScheduledTestCombinationRule{
		{ID: "route_a", Priority: 2, Condition: combinationTestLeaf(1, "pass"), Action: ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: []int64{30, 10}}},
		{ID: "route_b", Priority: 2, Condition: combinationTestLeaf(2, "pass"), Action: ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{20, 10}}},
		{ID: "pause", Priority: 2, Condition: combinationTestLeaf(3, "fail"), Action: ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}},
		{ID: "lower_priority", Priority: 1, Condition: combinationTestLeaf(1, "pass"), Action: ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{99}}},
	}
	decision := EvaluateScheduledTestCombinations(plan.Protection, map[int64]string{1: "pass", 2: "pass", 3: "fail"})
	require.NotNil(t, decision.Action)
	require.Equal(t, "pause", decision.Action.Scheduling, "pause wins over resume and keep at the same priority")
	require.Equal(t, "assign", decision.Action.GroupMode)
	require.Equal(t, []int64{10, 20, 30}, decision.Action.GroupIDs)
	require.ElementsMatch(t, []string{"route_a", "route_b", "pause"}, decision.RuleIDs)
	require.Equal(t, []int64{30, 10}, plan.Protection.Combinations[0].Action.GroupIDs, "evaluating must not reorder the saved configuration")
	decision.Action.GroupIDs[0] = 999
	require.Equal(t, []int64{30, 10}, plan.Protection.Combinations[0].Action.GroupIDs, "resolved groups must not alias the configuration")

	decision = EvaluateScheduledTestCombinations(plan.Protection, map[int64]string{1: "pass", 2: "pass", 3: "pass"})
	require.NotNil(t, decision.Action)
	require.Equal(t, "resume", decision.Action.Scheduling, "resume wins over keep when no matching pause exists")
	require.ElementsMatch(t, []string{"route_a", "route_b"}, decision.RuleIDs)
}

func TestScheduledTestCombinationsExplicitKeepWinsAtHigherPriority(t *testing.T) {
	plan := combinationTestPlan()
	plan.Protection.Combinations = append(plan.Protection.Combinations, ScheduledTestCombinationRule{
		ID: "hold", Priority: 2, Condition: combinationTestLeaf(3, "pass"),
		Action: ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"},
	})
	decision := EvaluateScheduledTestCombinations(plan.Protection, map[int64]string{1: "pass", 2: "pass", 3: "pass"})
	require.NotNil(t, decision.Action)
	require.Equal(t, ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}, *decision.Action)
	require.Equal(t, []string{"hold"}, decision.RuleIDs)
}

func TestScheduledTestCombinationsValidationRejectsInvalidConfiguration(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mutate func(*ScheduledTestPlan)
	}{
		{"unknown mode", func(p *ScheduledTestPlan) { p.Protection.Mode = "first_match" }},
		{"legacy mode cannot silently discard combinations", func(p *ScheduledTestPlan) { p.Protection.Mode = "" }},
		{"explicit legacy mode cannot discard combinations", func(p *ScheduledTestPlan) { p.Protection.Mode = "per_test" }},
		{"combined mode needs a rule", func(p *ScheduledTestPlan) { p.Protection.Combinations = nil }},
		{"too many combinations", func(p *ScheduledTestPlan) {
			base := p.Protection.Combinations[0]
			for i := 1; i <= 32; i++ {
				rule := base
				rule.ID = fmt.Sprintf("rule_%d", i)
				p.Protection.Combinations = append(p.Protection.Combinations, rule)
			}
		}},
		{"empty id", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].ID = "" }},
		{"id contains whitespace", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].ID = "rule one" }},
		{"id contains path delimiter", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].ID = "rule/one" }},
		{"id too long", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].ID = strings.Repeat("a", 65) }},
		{"duplicate id", func(p *ScheduledTestPlan) {
			p.Protection.Combinations = append(p.Protection.Combinations, p.Protection.Combinations[0])
		}},
		{"duplicate id after trimming", func(p *ScheduledTestPlan) {
			copy := p.Protection.Combinations[0]
			copy.ID = " " + copy.ID + " "
			p.Protection.Combinations = append(p.Protection.Combinations, copy)
		}},
		{"empty name", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Name = "" }},
		{"whitespace name", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Name = " \n\t " }},
		{"name too long", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Name = strings.Repeat("条", 101) }},
		{"negative priority", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Priority = -1 }},
		{"priority above limit", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Priority = 1001 }},
		{"unknown expression operator", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition.Operator = "not" }},
		{"empty all group", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestGroup("all") }},
		{"empty any group", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestGroup("any") }},
		{"group cannot include leaf id", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition.TestDefinitionID = 1 }},
		{"group cannot include leaf verdict", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition.Verdict = "pass" }},
		{"leaf cannot have children", func(p *ScheduledTestPlan) {
			p.Protection.Combinations[0].Condition = combinationTestLeaf(1, "pass")
			p.Protection.Combinations[0].Condition.Conditions = []ScheduledTestCombinationCondition{combinationTestLeaf(2, "pass")}
		}},
		{"missing leaf reference", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestLeaf(0, "pass") }},
		{"negative leaf reference", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestLeaf(-1, "pass") }},
		{"unselected leaf reference", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestLeaf(99, "pass") }},
		{"selected but unconfigured source reference", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestLeaf(4, "pass") }},
		{"pending is not a target verdict", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestLeaf(1, "pending") }},
		{"missing target verdict", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Condition = combinationTestLeaf(1, "") }},
		{"unknown scheduling", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Action.Scheduling = "disable" }},
		{"unknown group mode", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Action.GroupMode = "append" }},
		{"assign requires groups", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Action.GroupIDs = nil }},
		{"keep cannot include groups", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Action.GroupMode = "keep" }},
		{"zero destination", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Action.GroupIDs = []int64{0} }},
		{"unselected destination", func(p *ScheduledTestPlan) { p.Protection.Combinations[0].Action.GroupIDs = []int64{99} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := combinationTestPlan()
			tc.mutate(plan)
			require.Error(t, validateScheduledTestProtection(plan))
		})
	}
}

func TestScheduledTestCombinationsValidationBoundariesAndNormalization(t *testing.T) {
	plan := combinationTestPlan()
	plan.Protection.Combinations[0].ID = " " + strings.Repeat("a", 62) + "_- "
	plan.Protection.Combinations[0].Name = " \n" + strings.Repeat("条", 100) + " \n"
	plan.Protection.Combinations[0].Priority = 1000
	plan.Protection.Combinations[0].Action = ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{30, 10, 30}}
	require.NoError(t, validateScheduledTestProtection(plan))
	require.Equal(t, strings.Repeat("a", 62)+"_-", plan.Protection.Combinations[0].ID)
	require.Equal(t, strings.Repeat("条", 100), plan.Protection.Combinations[0].Name)
	require.Equal(t, "keep", plan.Protection.Combinations[0].Action.Scheduling)
	require.Equal(t, []int64{10, 30}, plan.Protection.Combinations[0].Action.GroupIDs)

	plan = combinationTestPlan()
	plan.Protection.Combinations[0].Action = ScheduledTestOutcomeAction{}
	require.NoError(t, validateScheduledTestProtection(plan))
	require.Equal(t, ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}, plan.Protection.Combinations[0].Action)

	for _, mode := range []string{"", "per_test"} {
		plan = protectionPlan()
		plan.Protection.Mode = mode
		require.NoError(t, validateScheduledTestProtection(plan), "existing per-test configurations remain valid")
	}

	plan = combinationTestPlan()
	base := plan.Protection.Combinations[0]
	for i := 1; i < 32; i++ {
		rule := base
		rule.ID = fmt.Sprintf("rule_%d", i)
		plan.Protection.Combinations = append(plan.Protection.Combinations, rule)
	}
	require.NoError(t, validateScheduledTestProtection(plan))
}

func TestScheduledTestCombinationsValidationDepthAndNodeLimits(t *testing.T) {
	for _, depth := range []int{6, 7} {
		t.Run(fmt.Sprintf("depth_%d", depth), func(t *testing.T) {
			plan := combinationTestPlan()
			condition := combinationTestLeaf(1, "pass")
			for i := 1; i < depth; i++ {
				condition = combinationTestGroup("all", condition)
			}
			plan.Protection.Combinations[0].Condition = condition
			if depth == 6 {
				require.NoError(t, validateScheduledTestProtection(plan))
			} else {
				require.Error(t, validateScheduledTestProtection(plan))
			}
		})
	}
	for _, nodes := range []int{128, 129} {
		t.Run(fmt.Sprintf("nodes_%d", nodes), func(t *testing.T) {
			plan := combinationTestPlan()
			leaves := make([]ScheduledTestCombinationCondition, nodes-1)
			for i := range leaves {
				leaves[i] = combinationTestLeaf(1, "pass")
			}
			plan.Protection.Combinations[0].Condition = combinationTestGroup("all", leaves...)
			if nodes == 128 {
				require.NoError(t, validateScheduledTestProtection(plan))
			} else {
				require.Error(t, validateScheduledTestProtection(plan))
			}
		})
	}
}

func TestScheduledTestCombinationsSourceExecutionFailureCanRemainPending(t *testing.T) {
	plan := combinationTestPlan()
	plan.Protection.Rules[0].PauseOnFailure = false
	plan.Protection.Rules[0].OnFail = nil
	verdict, _ := evaluateScheduledTestProtection(plan.Protection.Rules[0], &ScheduledTestResult{Status: "failed"})
	require.Equal(t, "pending", verdict)
	decision := EvaluateScheduledTestCombinations(plan.Protection, map[int64]string{1: verdict, 2: "pass"})
	require.Nil(t, decision.Action)

	plan.Protection.Rules[0].PauseOnFailure = true
	verdict, _ = evaluateScheduledTestProtection(plan.Protection.Rules[0], &ScheduledTestResult{Status: "failed"})
	require.Equal(t, "fail", verdict)
}

func TestScheduledTestCombinationsRequireEnabledCombinedMode(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mode    string
		enabled bool
	}{
		{"disabled", "combined", false},
		{"legacy mode", "", true},
		{"explicit legacy mode", "per_test", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := combinationTestPlan()
			plan.Protection.Mode = tc.mode
			plan.Protection.Enabled = tc.enabled
			decision := EvaluateScheduledTestCombinations(plan.Protection, map[int64]string{1: "pass", 2: "pass"})
			require.Nil(t, decision.Action)
			require.Empty(t, decision.RuleIDs)
		})
	}
}

func TestScheduledTestCombinationsPreventCacheRecoveryDeadlock(t *testing.T) {
	for _, tc := range []struct {
		name       string
		condition  ScheduledTestCombinationCondition
		scheduling string
		recovery   bool
		valid      bool
	}{
		{
			name:      "failed recovery source cannot add its own pause hold",
			condition: combinationTestLeaf(1, "fail"), scheduling: "pause", recovery: true,
		},
		{
			name: "nested recovery source cannot add a pause hold",
			condition: combinationTestGroup("any", combinationTestLeaf(2, "fail"),
				combinationTestGroup("all", combinationTestLeaf(3, "pass"), combinationTestLeaf(1, "fail"))),
			scheduling: "pause", recovery: true,
		},
		{
			name:      "pass condition cannot add a hold that obstructs later recovery",
			condition: combinationTestLeaf(1, "pass"), scheduling: "pause", recovery: true,
		},
		{
			name:      "recovery source may adjust groups while keeping scheduling",
			condition: combinationTestLeaf(1, "fail"), scheduling: "keep", recovery: true, valid: true,
		},
		{
			name:      "recovery source may release a combination hold",
			condition: combinationTestLeaf(1, "pass"), scheduling: "resume", recovery: true, valid: true,
		},
		{
			name:       "independent checks may pause even with recovery configured",
			condition:  combinationTestGroup("any", combinationTestLeaf(2, "fail"), combinationTestLeaf(3, "fail")),
			scheduling: "pause", recovery: true, valid: true,
		},
		{
			name:      "disabled independent recovery permits combination scheduling",
			condition: combinationTestLeaf(1, "fail"), scheduling: "pause", recovery: false, valid: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := combinationTestPlan()
			plan.Protection.Rules[0] = cacheRecoveryRule()
			plan.Protection.Rules[0].Recovery.Enabled = tc.recovery
			plan.Protection.Combinations[0].Condition = tc.condition
			plan.Protection.Combinations[0].Action.Scheduling = tc.scheduling
			err := validateScheduledTestProtection(plan)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "暂停动作不能引用已启用自动试运行的检测 #1")
				require.ErrorContains(t, err, plan.Protection.Combinations[0].Name)
				require.ErrorContains(t, err, "仅调整分组")
			}
		})
	}
}

func TestScheduledTestCombinationsPreventCrossRuleRecoveryDeadlock(t *testing.T) {
	for _, tc := range []struct {
		name            string
		resumeCondition ScheduledTestCombinationCondition
		withPause       bool
		resumeFirst     bool
		recoveryEnabled bool
		valid           bool
	}{
		{
			name:            "candy pause cannot wait for both candy and cache to recover",
			resumeCondition: combinationTestGroup("all", combinationTestLeaf(2, "pass"), combinationTestLeaf(1, "pass")),
			withPause:       true, recoveryEnabled: true,
		},
		{
			name:            "later pause still makes earlier recovery rule invalid",
			resumeCondition: combinationTestGroup("all", combinationTestLeaf(2, "pass"), combinationTestLeaf(1, "pass")),
			withPause:       true, resumeFirst: true, recoveryEnabled: true,
		},
		{
			name: "nested recovery dependency is rejected",
			resumeCondition: combinationTestGroup("all", combinationTestLeaf(2, "pass"),
				combinationTestGroup("any", combinationTestLeaf(3, "pass"), combinationTestLeaf(1, "pass"))),
			withPause: true, recoveryEnabled: true,
		},
		{
			name:            "resume cache dependency is allowed without any combination pause",
			resumeCondition: combinationTestGroup("all", combinationTestLeaf(2, "pass"), combinationTestLeaf(1, "pass")),
			recoveryEnabled: true, valid: true,
		},
		{
			name:            "candy can independently release its pause before cache trial",
			resumeCondition: combinationTestLeaf(2, "pass"),
			withPause:       true, recoveryEnabled: true, valid: true,
		},
		{
			name:            "disabled automatic trial permits combined recovery condition",
			resumeCondition: combinationTestGroup("all", combinationTestLeaf(2, "pass"), combinationTestLeaf(1, "pass")),
			withPause:       true, valid: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			plan := combinationTestPlan()
			plan.Protection.Rules[0] = cacheRecoveryRule()
			plan.Protection.Rules[0].Recovery.Enabled = tc.recoveryEnabled
			resume := ScheduledTestCombinationRule{
				ID: "resume", Name: "恢复账号", Priority: 1,
				Condition: tc.resumeCondition,
				Action:    ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"},
			}
			pause := ScheduledTestCombinationRule{
				ID: "candy_pause", Name: "糖果不通过暂停", Priority: 2,
				Condition: combinationTestLeaf(2, "fail"),
				Action:    ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"},
			}
			// Cache verdicts may still control groups after the independent
			// candy pause is released; only the scheduling dependency is unsafe.
			plan.Protection.Combinations[0].Condition = combinationTestLeaf(1, "pass")
			plan.Protection.Combinations = append(plan.Protection.Combinations, resume)
			if tc.withPause {
				if tc.resumeFirst {
					plan.Protection.Combinations = append(plan.Protection.Combinations, pause)
				} else {
					plan.Protection.Combinations = append([]ScheduledTestCombinationRule{pause}, plan.Protection.Combinations...)
				}
			}
			err := validateScheduledTestProtection(plan)
			if tc.valid {
				require.NoError(t, err)
			} else {
				require.ErrorContains(t, err, "恢复动作不能依赖已启用自动试运行的检测 #1")
				require.ErrorContains(t, err, resume.Name)
				require.ErrorContains(t, err, "分组规则拆开")
			}
		})
	}
}
