package service

import (
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestScheduledTestAllPassConfigurableChecks(t *testing.T) {
	for _, count := range []int{1, 5, 30} {
		t.Run(fmt.Sprintf("%d automatic checks", count), func(t *testing.T) {
			p := &ScheduledTestPlan{Protection: ScheduledTestProtectionConfig{Enabled: true, Mode: "all_pass"}}
			verdicts := map[int64]string{9001: "fail"} // A display-only failure must not affect scheduling.
			p.TestDefinitionIDs = append(p.TestDefinitionIDs, 9001)
			for i := 0; i < count; i++ {
				id := int64(101 + i*17)
				p.TestDefinitionIDs = append(p.TestDefinitionIDs, id)
				p.Protection.Rules = append(p.Protection.Rules, ScheduledTestProtectionRule{TestDefinitionID: id})
				verdicts[id] = "pass"
			}
			p.TestDefinitionIDs = append(p.TestDefinitionIDs, 7001)
			p.Protection.Rules = append(p.Protection.Rules, ScheduledTestProtectionRule{
				TestDefinitionID: 7001, Vote: &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1},
			})
			require.NoError(t, validateScheduledTestProtection(p))
			assertScheduling := func(want string, held bool) {
				t.Helper()
				decision, _ := EvaluateScheduledTestAllPass(p.Protection, verdicts, nil, held)
				require.Equal(t, want, decision.Action.Scheduling)
			}
			assertScheduling("resume", true) // Any number of automatic checks; manual review may be pending.
			last := p.Protection.Rules[count-1].TestDefinitionID
			delete(verdicts, last)
			assertScheduling("keep", false)
			assertScheduling("pause", true)
			verdicts[last] = "fail"
			assertScheduling("pause", false)
			verdicts[last] = "pass"
			assertScheduling("resume", true)
			verdicts[7001] = "fail"
			assertScheduling("pause", false)
			// Changing the review to display-only removes its veto even if an old verdict remains.
			p.Protection.Rules = p.Protection.Rules[:count]
			assertScheduling("resume", true)
			// Turning that check into an automatic requirement makes its result mandatory.
			p.Protection.Rules = append(p.Protection.Rules, ScheduledTestProtectionRule{TestDefinitionID: 7001})
			delete(verdicts, 7001)
			assertScheduling("pause", true)
			verdicts[7001] = "pass"
			assertScheduling("resume", true)
		})
	}
}

func allPassTestPlan() *ScheduledTestPlan {
	p := combinationTestPlan()
	p.Protection.Mode, p.Protection.Combinations = "all_pass", nil
	p.Protection.PassGroupIDs = []int64{10}
	p.Protection.Rules = append(p.Protection.Rules, ScheduledTestProtectionRule{TestDefinitionID: 4, Vote: &ScheduledTestVoteConfig{Enabled: true, PassAtLeast: 1}})
	return p
}

func TestScheduledTestAllPassDecisions(t *testing.T) {
	for _, tc := range []struct {
		name           string
		verdicts       []string
		held, recovery bool
		want           string
		assign         bool
	}{
		{"pending active", []string{"pass", "pending", "pass", "pending"}, false, false, "keep", false},
		{"pending paused", []string{"pass", "pending", "pass", "pass"}, true, false, "pause", false},
		{"failure before completion", []string{"fail", "pending", "pending", "pending"}, false, false, "pause", false},
		{"no review needed", []string{"pass", "pass", "pass", "pending"}, true, false, "resume", true},
		{"manual veto", []string{"pass", "pass", "pass", "fail"}, false, false, "pause", false},
		{"manual approval cannot bypass", []string{"pass", "fail", "pass", "pass"}, true, false, "pause", false},
		{"recovery trial gate", []string{"pass", "pass", "fail", "pending"}, true, true, "resume", false},
		{"pending blocks trial", []string{"pending", "pass", "fail", "pending"}, true, true, "pause", false},
		{"manual rejection blocks trial", []string{"pass", "pass", "fail", "fail"}, true, true, "pause", false},
		{"automatic failure blocks trial", []string{"pass", "fail", "fail", "pass"}, true, true, "pause", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := allPassTestPlan()
			verdicts, recovering := map[int64]string{}, map[int64]bool{}
			for i, verdict := range tc.verdicts {
				verdicts[int64(i+1)] = verdict
			}
			if tc.recovery {
				p.Protection.Rules[2].Recovery = &ScheduledTestCacheRecoveryConfig{Enabled: true}
				recovering[3] = true
			}
			decision, _ := EvaluateScheduledTestAllPass(p.Protection, verdicts, recovering, tc.held)
			require.Equal(t, tc.want, decision.Action.Scheduling)
			require.Equal(t, tc.assign, decision.Action.GroupMode == "assign")
			if tc.assign {
				require.Equal(t, []int64{10}, decision.Action.GroupIDs)
			}
		})
	}
}

func TestScheduledTestAllPassValidation(t *testing.T) {
	p := allPassTestPlan()
	p.Protection.Rules[0].Priority = 1000
	p.Protection.Rules[0].RequiredPass = true
	p.Protection.Rules[0].OnPass = &ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{999}}
	require.NoError(t, validateScheduledTestProtection(p))
	require.Zero(t, p.Protection.Rules[0].Priority)
	require.False(t, p.Protection.Rules[0].RequiredPass)
	require.Equal(t, &ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}, p.Protection.Rules[0].OnPass)
	require.Equal(t, []int64{10}, p.ProtectionActionGroupIDs())
	for _, tc := range []struct {
		name   string
		change func(*ScheduledTestPlan)
	}{
		{"public review", func(p *ScheduledTestPlan) { p.Protection.Rules[3].Vote.PublicEnabled = true }},
		{"only review", func(p *ScheduledTestPlan) { p.Protection.Rules = p.Protection.Rules[3:] }},
		{"outside group", func(p *ScheduledTestPlan) { p.Protection.PassGroupIDs = []int64{999} }},
		{"extra combinations", func(p *ScheduledTestPlan) { p.Protection.Combinations = combinationTestPlan().Protection.Combinations }},
		{"groups outside mode", func(p *ScheduledTestPlan) { p.Protection.Mode = "per_test" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := allPassTestPlan()
			tc.change(p)
			require.Error(t, validateScheduledTestProtection(p))
		})
	}
}

func TestScheduledTestAllPassOutcomeActions(t *testing.T) {
	for _, passScheduling := range []string{"keep", "pause", "resume"} {
		for _, failScheduling := range []string{"keep", "pause", "resume"} {
			t.Run(passScheduling+"/"+failScheduling, func(t *testing.T) {
				p := allPassTestPlan()
				p.Protection.OnPass = &ScheduledTestOutcomeAction{Scheduling: passScheduling, GroupMode: "assign", GroupIDs: []int64{10}}
				p.Protection.OnFail = &ScheduledTestOutcomeAction{Scheduling: failScheduling, GroupMode: "assign", GroupIDs: []int64{20}}
				require.NoError(t, validateScheduledTestProtection(p))
				require.Empty(t, p.Protection.PassGroupIDs)
				require.Equal(t, []int64{10, 20}, p.ProtectionActionGroupIDs())
				verdicts := map[int64]string{1: "pass", 2: "pass", 3: "pass"}
				decision, _ := EvaluateScheduledTestAllPass(p.Protection, verdicts, nil, true)
				require.Equal(t, p.Protection.OnPass, decision.Action)
				decision.Action.GroupIDs[0] = 999
				require.Equal(t, []int64{10}, p.Protection.OnPass.GroupIDs)
				verdicts[2], verdicts[3] = "fail", "pending"
				decision, _ = EvaluateScheduledTestAllPass(p.Protection, verdicts, nil, false)
				require.Equal(t, p.Protection.OnFail, decision.Action)
				verdicts[2], verdicts[3], verdicts[4] = "pass", "pass", "fail"
				decision, _ = EvaluateScheduledTestAllPass(p.Protection, verdicts, nil, false)
				require.Equal(t, p.Protection.OnFail, decision.Action, "administrator veto uses the failure action")
				delete(verdicts, 4)
				verdicts[3] = "pending"
				for _, held := range []bool{false, true} {
					decision, _ = EvaluateScheduledTestAllPass(p.Protection, verdicts, nil, held)
					want := "keep"
					if held {
						want = "pause"
					}
					require.Equal(t, &ScheduledTestOutcomeAction{Scheduling: want, GroupMode: "keep"}, decision.Action)
				}
			})
		}
	}
}

func TestScheduledTestAllPassOutcomeActionValidation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*ScheduledTestPlan)
	}{
		{"invalid scheduling", func(p *ScheduledTestPlan) { p.Protection.OnFail = &ScheduledTestOutcomeAction{Scheduling: "invalid"} }},
		{"invalid group mode", func(p *ScheduledTestPlan) { p.Protection.OnPass = &ScheduledTestOutcomeAction{GroupMode: "invalid"} }},
		{"empty failure assignment", func(p *ScheduledTestPlan) { p.Protection.OnFail = &ScheduledTestOutcomeAction{GroupMode: "assign"} }},
		{"unselected failure group", func(p *ScheduledTestPlan) {
			p.Protection.OnFail = &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{999}}
		}},
		{"keep with groups", func(p *ScheduledTestPlan) {
			p.Protection.OnPass = &ScheduledTestOutcomeAction{GroupMode: "keep", GroupIDs: []int64{10}}
		}},
		{"actions outside mode", func(p *ScheduledTestPlan) {
			p.Protection.PassGroupIDs = nil
			p.Protection.Mode = "per_test"
			p.Protection.OnFail = &ScheduledTestOutcomeAction{Scheduling: "keep"}
		}},
		{"recovery with keep on fail", func(p *ScheduledTestPlan) {
			p.Protection.OnFail = &ScheduledTestOutcomeAction{Scheduling: "keep"}
			p.Protection.Rules[2].Recovery = &ScheduledTestCacheRecoveryConfig{Enabled: true}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := allPassTestPlan()
			tc.change(p)
			require.Error(t, validateScheduledTestProtection(p))
		})
	}
	p := allPassTestPlan()
	p.Protection.OnPass = &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}
	p.Protection.OnFail = &ScheduledTestOutcomeAction{GroupMode: "assign", GroupIDs: []int64{10, 20, 10}}
	require.NoError(t, validateScheduledTestProtection(p))
	require.Empty(t, p.Protection.PassGroupIDs, "explicit keep overrides legacy group assignment")
	require.Equal(t, []int64{10, 20}, p.Protection.OnFail.GroupIDs)
	require.Equal(t, "keep", p.Protection.OnFail.Scheduling)
}

func TestScheduledTestAllPassRecoveryFailureGroups(t *testing.T) {
	p := allPassTestPlan()
	p.Protection.OnFail = &ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "assign", GroupIDs: []int64{20}}
	p.Protection.Rules[2].Recovery = &ScheduledTestCacheRecoveryConfig{Enabled: true}
	recovering := map[int64]bool{3: true}
	verdicts := map[int64]string{1: "pass", 2: "pending", 3: "fail"}
	decision, _ := EvaluateScheduledTestAllPass(p.Protection, verdicts, recovering, true)
	require.Equal(t, p.Protection.OnFail, decision.Action)
	verdicts[2] = "pass"
	decision, _ = EvaluateScheduledTestAllPass(p.Protection, verdicts, recovering, true)
	require.Equal(t, "resume", decision.Action.Scheduling)
	require.Equal(t, []int64{20}, decision.Action.GroupIDs, "trial traffic uses failure groups until statistics recover")
	verdicts[3] = "pass"
	decision, _ = EvaluateScheduledTestAllPass(p.Protection, verdicts, nil, true)
	require.Equal(t, []int64{10}, decision.Action.GroupIDs)
}

func TestScheduledTestAllPassUsesLatestPassRateVerdict(t *testing.T) {
	for _, mode := range []string{"all_pass", "per_test"} {
		t.Run(mode, func(t *testing.T) {
			gate := &ScheduledTestPassRateConfig{Enabled: true, WindowSize: 10, WindowSeconds: 604800, PauseBelow: 80, RecoverAtLeast: 90, RecoverConsecutive: 3}
			plan := &ScheduledTestPlan{TestDefinitionIDs: []int64{1}, Protection: ScheduledTestProtectionConfig{Enabled: true, Mode: mode, Rules: []ScheduledTestProtectionRule{{TestDefinitionID: 1, ExpectedAnswer: "21", PassRate: gate}}}}
			require.NoError(t, validateScheduledTestProtection(plan))
			if mode == "all_pass" {
				require.Equal(t, 1, gate.WindowSize)
				require.Equal(t, 1, gate.RecoverConsecutive)
				require.Equal(t, 100., gate.PauseBelow)
				require.Equal(t, 100., gate.RecoverAtLeast)
				require.True(t, gate.PauseOnSingleFailure)
			} else {
				require.Equal(t, 10, gate.WindowSize)
				require.Equal(t, 3, gate.RecoverConsecutive)
			}
			require.Equal(t, "21", plan.Protection.Rules[0].ExpectedAnswer)
		})
	}
}
