package service

import (
	"fmt"
	"slices"
)

func (c ScheduledTestProtectionConfig) UsesAllPass() bool { return c.Mode == "all_pass" }

// AllPassOutcomeAction preserves the defaults and group targets of older plans.
// Explicit outcome actions take precedence over the legacy pass_group_ids field.
func (c ScheduledTestProtectionConfig) AllPassOutcomeAction(verdict string) ScheduledTestOutcomeAction {
	rule := ScheduledTestProtectionRule{OnPass: c.OnPass, OnFail: c.OnFail}
	if rule.OnPass == nil {
		rule.OnPass = &ScheduledTestOutcomeAction{Scheduling: "resume", GroupMode: "keep"}
		if len(c.PassGroupIDs) > 0 {
			rule.OnPass.GroupMode, rule.OnPass.GroupIDs = "assign", c.PassGroupIDs
		}
	}
	if rule.OnFail == nil {
		rule.OnFail = &ScheduledTestOutcomeAction{Scheduling: "pause", GroupMode: "keep"}
	}
	return rule.OutcomeAction(verdict)
}

// Actions are evaluated once for the whole policy, after its requirements resolve.
func validateScheduledTestAllPass(plan *ScheduledTestPlan) error {
	c := &plan.Protection
	if !c.UsesAllPass() {
		if len(c.PassGroupIDs) > 0 || c.OnPass != nil || c.OnFail != nil {
			return fmt.Errorf("policy outcome actions require all_pass protection mode")
		}
		return nil
	}
	if err := validateScheduledTestActions(&ScheduledTestProtectionRule{OnPass: c.OnPass, OnFail: c.OnFail}); err != nil {
		return err
	}
	if c.OnPass != nil {
		c.PassGroupIDs = nil
	}
	pass, fail := c.AllPassOutcomeAction("pass"), c.AllPassOutcomeAction("fail")
	if err := validateScheduledTestActions(&ScheduledTestProtectionRule{OnPass: &pass, OnFail: &fail}); err != nil {
		return err
	}
	for _, action := range []ScheduledTestOutcomeAction{pass, fail} {
		for _, id := range action.GroupIDs {
			if !slices.Contains(plan.GroupIDs, id) {
				return fmt.Errorf("action group %d must be selected in group_ids", id)
			}
		}
	}
	if c.OnPass == nil {
		c.PassGroupIDs = slices.Clone(pass.GroupIDs)
	}
	automatic := 0
	for i := range c.Rules {
		r := &c.Rules[i]
		r.Priority, r.RequiredPass, r.PauseOnFailure = 0, false, true
		r.OnPass = &ScheduledTestOutcomeAction{Scheduling: pass.Scheduling, GroupMode: "keep"}
		r.OnFail = &ScheduledTestOutcomeAction{Scheduling: fail.Scheduling, GroupMode: "keep"}
		if r.Vote == nil || !r.Vote.Enabled || r.HasDurationThreshold() || r.UsesAutoReview() {
			automatic++
		}
		if r.Vote != nil && r.Vote.Enabled && r.Vote.PublicEnabled {
			return fmt.Errorf("all_pass review is administrator-only")
		}
	}
	if automatic == 0 {
		return fmt.Errorf("all_pass requires at least one automatic check")
	}
	return nil
}

// Pending checks preserve active accounts, but cannot release an existing pause.
// Review verdicts also include any explicitly configured duration condition.
func EvaluateScheduledTestAllPass(config ScheduledTestProtectionConfig, verdicts map[int64]string, _ map[int64]bool, held bool) (ScheduledTestCombinationDecision, string) {
	decision := ScheduledTestCombinationDecision{Action: &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}}
	if !config.Enabled || !config.UsesAllPass() {
		return decision, ""
	}
	pending, automatic := false, 0
	for _, rule := range config.Rules {
		id := rule.TestDefinitionID
		if verdicts[id] == "fail" {
			action := config.AllPassOutcomeAction("fail")
			decision.Action = &action
			return decision, "自动检测或人工评审不通过，执行不通过动作"
		}
		if rule.Vote == nil || !rule.Vote.Enabled || rule.HasDurationThreshold() || rule.UsesAutoReview() {
			automatic++
			pending = pending || verdicts[id] != "pass"
		}
	}
	if pending || automatic == 0 {
		if held {
			decision.Action.Scheduling = "pause"
		}
		return decision, "等待本轮全部自动检测通过，保留已有暂停"
	}
	action := config.AllPassOutcomeAction("pass")
	decision.Action = &action
	return decision, "全部自动检测通过，且无本轮人工否决"
}
