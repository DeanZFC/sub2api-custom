package service

import "fmt"

// GroupWorkflow is an explicit alternative to the default all-checks-must-pass
// policy. The number check places an account first; an administrator can then
// override that placement for this round. Each placement replaces all groups.
type ScheduledTestGroupWorkflow struct {
	AutomaticTestID int64 `json:"automatic_test_id"`
	ReviewTestID    int64 `json:"review_test_id"`
	PassGroupID     int64 `json:"pass_group_id"`
	FailGroupID     int64 `json:"fail_group_id"`
}

func (c *ScheduledTestGroupWorkflow) Rules() []ScheduledTestProtectionRule {
	action := func(groupID int64) *ScheduledTestOutcomeAction {
		return &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "assign", GroupIDs: []int64{groupID}}
	}
	return []ScheduledTestProtectionRule{
		{TestDefinitionID: c.AutomaticTestID, PauseOnFailure: true, ExpectedAnswer: "21", AnswerMatch: "numeric", OnPass: action(c.PassGroupID), OnFail: action(c.FailGroupID)},
		{TestDefinitionID: c.ReviewTestID, PauseOnFailure: true, AnswerMatch: "exact", Vote: &ScheduledTestVoteConfig{Enabled: true, RejectAbove: 0, PassAtLeast: 1}, OnPass: action(c.PassGroupID), OnFail: action(c.FailGroupID)},
	}
}

func normalizeScheduledTestGroupWorkflow(plan *ScheduledTestPlan) error {
	c := plan.Protection.GroupWorkflow
	if c == nil {
		return nil
	}
	if plan.TargetMode != "all_accounts" || plan.AccountID != nil {
		return fmt.Errorf("group workflow requires all_accounts targeting both workflow groups")
	}
	if c.AutomaticTestID <= 0 || c.ReviewTestID <= 0 || c.AutomaticTestID == c.ReviewTestID {
		return fmt.Errorf("group workflow requires distinct automatic and review tests")
	}
	if c.PassGroupID <= 0 || c.FailGroupID <= 0 || c.PassGroupID == c.FailGroupID {
		return fmt.Errorf("group workflow requires distinct pass and fail groups")
	}
	if plan.GroupID == nil || (*plan.GroupID != c.PassGroupID && *plan.GroupID != c.FailGroupID) {
		return fmt.Errorf("group workflow source must be one of its two groups")
	}
	// Canonical rules prevent a hidden legacy pause, answer or vote requirement
	// from changing the meaning of the dedicated editor. Opting out removes
	// GroupWorkflow and returns to ordinary protection configuration.
	plan.TestDefinitionIDs = []int64{c.AutomaticTestID, c.ReviewTestID}
	automaticID := c.AutomaticTestID
	plan.TestDefinitionID = &automaticID
	plan.Protection.Rules = c.Rules()
	plan.AutoRecover = false
	return nil
}

func validateScheduledTestGroupWorkflowDefinition(plan *ScheduledTestPlan, d *ScheduledTestDefinition) error {
	c := plan.Protection.GroupWorkflow
	if c == nil || !plan.Protection.Enabled {
		return nil
	}
	if d.ID == c.AutomaticTestID && d.OutputKind != "number" {
		return fmt.Errorf("group workflow automatic test must produce a number")
	}
	if d.ID == c.ReviewTestID && d.OutputKind != "html" {
		return fmt.Errorf("group workflow review test must produce HTML or SVG")
	}
	return nil
}
