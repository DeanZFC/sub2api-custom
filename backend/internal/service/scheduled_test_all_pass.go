package service

import (
	"fmt"
	"slices"
)

func (c ScheduledTestProtectionConfig) UsesAllPass() bool { return c.Mode == "all_pass" }

// This mode has one fixed policy, rather than separately configurable actions
// that can accidentally resume before another required check has completed.
func validateScheduledTestAllPass(plan *ScheduledTestPlan) error {
	c := &plan.Protection
	if !c.UsesAllPass() {
		if len(c.PassGroupIDs) > 0 {
			return fmt.Errorf("pass_group_ids require all_pass protection mode")
		}
		return nil
	}
	automatic := 0
	for i := range c.Rules {
		r := &c.Rules[i]
		r.Priority, r.RequiredPass, r.PauseOnFailure = 0, false, true
		r.OnPass = &ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
		r.OnFail = &ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
		if r.Vote == nil || !r.Vote.Enabled {
			automatic++
		} else if r.Vote.PublicEnabled {
			return fmt.Errorf("all_pass review is administrator-only")
		}
	}
	if automatic == 0 {
		return fmt.Errorf("all_pass requires at least one automatic check")
	}
	if len(c.PassGroupIDs) > 0 {
		action := ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "assign", GroupIDs: c.PassGroupIDs}
		if err := validateScheduledTestActions(&ScheduledTestProtectionRule{OnPass: &action}); err != nil {
			return err
		}
		for _, id := range action.GroupIDs {
			if !slices.Contains(plan.GroupIDs, id) {
				return fmt.Errorf("pass group %d must be selected in group_ids", id)
			}
		}
		c.PassGroupIDs = action.GroupIDs
	}
	return nil
}

// Recovering checks retain their independent hold and request budget. Releasing
// the policy hold only permits a bounded trial; it never grants full recovery.
// Pending checks preserve active accounts, but cannot release an existing pause.
// For review rules, verdicts contains only the current administrator decision.
func EvaluateScheduledTestAllPass(config ScheduledTestProtectionConfig, verdicts map[int64]string, recovering map[int64]bool, held bool) (ScheduledTestCombinationDecision, string) {
	decision := ScheduledTestCombinationDecision{Action: &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}}
	if !config.Enabled || !config.UsesAllPass() {
		return decision, ""
	}
	pending, trial, automatic := false, false, 0
	for _, rule := range config.Rules {
		id := rule.TestDefinitionID
		if rule.Recovery != nil && rule.Recovery.Enabled && recovering[id] {
			trial = true
			continue
		}
		if verdicts[id] == "fail" {
			decision.Action.Scheduling = "pause"
			return decision, "自动检测或人工评审不通过，暂停调用"
		}
		if rule.Vote == nil || !rule.Vote.Enabled {
			automatic++
			pending = pending || verdicts[id] != "pass"
		}
	}
	if pending || (automatic == 0 && !trial) {
		if held {
			decision.Action.Scheduling = "pause"
		}
		return decision, "等待本轮全部自动检测通过，保留已有暂停"
	}
	decision.Action.Scheduling = "resume"
	if trial {
		return decision, "其他自动检测已通过，等待统计限量试运行达标"
	}
	if len(config.PassGroupIDs) > 0 {
		decision.Action.GroupMode = "assign"
		decision.Action.GroupIDs = slices.Clone(config.PassGroupIDs)
	}
	return decision, "全部自动检测通过，且无本轮人工否决"
}
