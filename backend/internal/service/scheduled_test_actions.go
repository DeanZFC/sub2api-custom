package service

import (
	"fmt"
	"slices"
)

// ScheduledTestOutcomeAction applies after an automatic or voting verdict has
// been resolved. Assign only manages the union of the rule's pass/fail groups;
// account memberships outside that scope must remain unchanged.
type ScheduledTestOutcomeAction struct {
	Scheduling string  `json:"scheduling"`
	GroupMode  string  `json:"group_mode"`
	GroupIDs   []int64 `json:"group_ids,omitempty"`
}

// OutcomeAction preserves the original pause/recover behavior for stored rules
// that predate outcome actions. An explicit empty action means do nothing.
// Inconclusive observations never authorize either action.
func (r ScheduledTestProtectionRule) OutcomeAction(verdict string) ScheduledTestOutcomeAction {
	action := ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}
	var configured *ScheduledTestOutcomeAction
	switch verdict {
	case "pass":
		configured = r.OnPass
		if configured == nil {
			action.Scheduling = "resume"
			return action
		}
	case "fail":
		configured = r.OnFail
		if configured == nil {
			action.Scheduling = "pause"
			return action
		}
	default:
		return action
	}
	if configured.Scheduling != "" {
		action.Scheduling = configured.Scheduling
	}
	if configured.GroupMode != "" {
		action.GroupMode = configured.GroupMode
	}
	action.GroupIDs = slices.Clone(configured.GroupIDs)
	return action
}

// ManagedGroupIDs is deterministic so callers can safely use it for locking,
// reconciliation, and persisted configuration comparisons.
func (r ScheduledTestProtectionRule) ManagedGroupIDs() []int64 {
	var ids []int64
	for _, action := range []*ScheduledTestOutcomeAction{r.OnPass, r.OnFail} {
		if action == nil || action.GroupMode != "assign" {
			continue
		}
		ids = append(ids, action.GroupIDs...)
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}

// HasGroupActions lets the runner retain an account as a detection target after
// an action moves it out of the plan's original group.
func (p *ScheduledTestPlan) HasGroupActions() bool {
	if p == nil || !p.Protection.Enabled {
		return false
	}
	for _, rule := range p.Protection.Rules {
		if len(rule.ManagedGroupIDs()) > 0 {
			return true
		}
	}
	return false
}

func validateScheduledTestActions(rule *ScheduledTestProtectionRule) error {
	hasAssignment := false
	for _, entry := range []struct {
		name   string
		action *ScheduledTestOutcomeAction
	}{{"on_pass", rule.OnPass}, {"on_fail", rule.OnFail}} {
		action := entry.action
		if action == nil {
			continue
		}
		if action.Scheduling == "" {
			action.Scheduling = "keep"
		}
		switch action.Scheduling {
		case "keep", "pause", "resume":
		default:
			return fmt.Errorf("%s scheduling must be keep, pause, or resume", entry.name)
		}
		if action.GroupMode == "" {
			action.GroupMode = "keep"
		}
		switch action.GroupMode {
		case "keep":
			if len(action.GroupIDs) != 0 {
				return fmt.Errorf("%s group_ids require assign group_mode", entry.name)
			}
		case "assign":
			hasAssignment = true
		default:
			return fmt.Errorf("%s group_mode must be keep or assign", entry.name)
		}
		for _, id := range action.GroupIDs {
			if id <= 0 {
				return fmt.Errorf("%s group_ids must be positive", entry.name)
			}
		}
		// Store a canonical set while leaving caller-owned slice backing arrays
		// intact. The limit concerns selected groups, not duplicate JSON entries.
		ids := slices.Clone(action.GroupIDs)
		slices.Sort(ids)
		ids = slices.Compact(ids)
		if len(ids) > 100 {
			return fmt.Errorf("%s supports at most 100 distinct groups", entry.name)
		}
		action.GroupIDs = ids
	}
	if hasAssignment && len(rule.ManagedGroupIDs()) == 0 {
		return fmt.Errorf("group assignment requires at least one group across on_pass and on_fail")
	}
	return nil
}
