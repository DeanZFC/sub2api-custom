package service

import (
	"fmt"
	"regexp"
	"slices"
	"strings"
)

// Conditions use only current-round completed verdicts. A missing observation
// is pending, including when a leaf asks whether a test failed.
type ScheduledTestCombinationCondition struct {
	Operator         string                              `json:"operator"`
	Conditions       []ScheduledTestCombinationCondition `json:"conditions,omitempty"`
	TestDefinitionID int64                               `json:"test_definition_id,omitempty"`
	Verdict          string                              `json:"verdict,omitempty"`
}

type ScheduledTestCombinationRule struct {
	ID        string                            `json:"id"`
	Name      string                            `json:"name"`
	Priority  int                               `json:"priority"`
	Condition ScheduledTestCombinationCondition `json:"condition"`
	Action    ScheduledTestOutcomeAction        `json:"action"`
}

type ScheduledTestCombinationDecision struct {
	Action  *ScheduledTestOutcomeAction
	RuleIDs []string
}

func (c ScheduledTestProtectionConfig) UsesCombinations() bool { return c.Mode == "combined" }

var scheduledTestCombinationID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)

func validateScheduledTestCombinations(plan *ScheduledTestPlan) error {
	c := &plan.Protection
	if !c.UsesCombinations() {
		if len(c.Combinations) != 0 {
			return fmt.Errorf("combination rules require combined protection mode")
		}
		return nil
	}
	if len(c.Combinations) == 0 || len(c.Combinations) > 32 {
		return fmt.Errorf("combined protection requires between 1 and 32 combination rules")
	}
	definitions := map[int64]bool{}
	recoveryDefinitions := map[int64]bool{}
	for _, rule := range c.Rules {
		definitions[rule.TestDefinitionID] = true
		if rule.Recovery != nil && rule.Recovery.Enabled {
			recoveryDefinitions[rule.TestDefinitionID] = true
		}
	}
	hasCombinationPause := false
	for _, rule := range c.Combinations {
		if rule.Action.Scheduling == "pause" {
			hasCombinationPause = true
			break
		}
	}
	ids := map[string]bool{}
	for i := range c.Combinations {
		rule := &c.Combinations[i]
		rule.ID, rule.Name = strings.TrimSpace(rule.ID), strings.TrimSpace(rule.Name)
		if !scheduledTestCombinationID.MatchString(rule.ID) || ids[rule.ID] {
			return fmt.Errorf("combination rule IDs must be distinct, 1..64 characters, using letters, numbers, underscore or hyphen")
		}
		ids[rule.ID] = true
		if rule.Name == "" || len([]rune(rule.Name)) > 100 {
			return fmt.Errorf("combination rule name must be between 1 and 100 characters")
		}
		if rule.Priority < 0 || rule.Priority > 1000 {
			return fmt.Errorf("combination priority must be between 0 and 1000")
		}
		nodes := 0
		if err := validateScheduledTestCombinationCondition(rule.Condition, definitions, 1, &nodes); err != nil {
			return fmt.Errorf("combination %s: %w", rule.ID, err)
		}
		if err := validateScheduledTestActions(&ScheduledTestProtectionRule{OnPass: &rule.Action}); err != nil {
			return fmt.Errorf("combination %s: %w", rule.ID, err)
		}
		// A second hold derived from a recovering source would prevent its
		// bounded trial from starting, so that source could never pass to
		// release the combination hold. Keep its recovery state authoritative.
		if rule.Action.Scheduling == "pause" {
			if id := scheduledTestCombinationRecoveryReference(rule.Condition, recoveryDefinitions); id != 0 {
				return fmt.Errorf("组合规则「%s」的暂停动作不能引用已启用自动试运行的检测 #%d：该检测已独立负责暂停和恢复；请将组合动作改为仅调整分组，或关闭该检测的自动试运行后再由组合控制调度", rule.Name, id)
			}
		}
		// A pause from an unrelated source is safe only when its matching
		// release can be resolved without waiting for the blocked trial.
		if hasCombinationPause && rule.Action.Scheduling == "resume" {
			if id := scheduledTestCombinationRecoveryReference(rule.Condition, recoveryDefinitions); id != 0 {
				return fmt.Errorf("组合规则「%s」的恢复动作不能依赖已启用自动试运行的检测 #%d：本策略配置了组合暂停，会阻止该检测试运行，造成相互等待；请将解除组合暂停与缓存达标后的分组规则拆开，或关闭该检测的自动试运行", rule.Name, id)
			}
		}
		for _, id := range rule.Action.GroupIDs {
			if !slices.Contains(plan.GroupIDs, id) {
				return fmt.Errorf("combination action group %d must be selected in group_ids", id)
			}
		}
	}
	return nil
}

func scheduledTestCombinationRecoveryReference(condition ScheduledTestCombinationCondition, recoveryDefinitions map[int64]bool) int64 {
	if condition.Operator == "test" && recoveryDefinitions[condition.TestDefinitionID] {
		return condition.TestDefinitionID
	}
	for _, child := range condition.Conditions {
		if id := scheduledTestCombinationRecoveryReference(child, recoveryDefinitions); id != 0 {
			return id
		}
	}
	return 0
}

func validateScheduledTestCombinationCondition(c ScheduledTestCombinationCondition, definitions map[int64]bool, depth int, nodes *int) error {
	*nodes++
	if depth > 6 || *nodes > 128 {
		return fmt.Errorf("conditions support at most 6 levels and 128 nodes per rule")
	}
	switch c.Operator {
	case "test":
		if !definitions[c.TestDefinitionID] || (c.Verdict != "pass" && c.Verdict != "fail") || len(c.Conditions) != 0 {
			return fmt.Errorf("test conditions require a configured detection, pass or fail, and no child conditions")
		}
	case "all", "any":
		if len(c.Conditions) == 0 || c.TestDefinitionID != 0 || c.Verdict != "" {
			return fmt.Errorf("all/any groups require child conditions and no test fields")
		}
		for _, child := range c.Conditions {
			if err := validateScheduledTestCombinationCondition(child, definitions, depth+1, nodes); err != nil {
				return err
			}
		}
	default:
		return fmt.Errorf("condition operator must be all, any, or test")
	}
	return nil
}

// EvaluateScheduledTestCombinationCondition returns pass for a matched
// expression, fail for a disproven expression, and pending for unknown data.
func EvaluateScheduledTestCombinationCondition(condition ScheduledTestCombinationCondition, verdicts map[int64]string) string {
	nodes := 0
	return evaluateScheduledTestCombinationCondition(condition, verdicts, 1, &nodes)
}

func evaluateScheduledTestCombinationCondition(c ScheduledTestCombinationCondition, verdicts map[int64]string, depth int, nodes *int) string {
	*nodes++
	if depth > 6 || *nodes > 128 {
		return "pending"
	}
	if c.Operator == "test" {
		if c.TestDefinitionID <= 0 || len(c.Conditions) > 0 || (c.Verdict != "pass" && c.Verdict != "fail") {
			return "pending"
		}
		verdict := verdicts[c.TestDefinitionID]
		if verdict != "pass" && verdict != "fail" {
			return "pending"
		}
		if verdict == c.Verdict {
			return "pass"
		}
		return "fail"
	}
	if (c.Operator != "all" && c.Operator != "any") || len(c.Conditions) == 0 || c.TestDefinitionID != 0 || c.Verdict != "" {
		return "pending"
	}
	passed, failed, pending := false, false, false
	for _, child := range c.Conditions {
		switch evaluateScheduledTestCombinationCondition(child, verdicts, depth+1, nodes) {
		case "pass":
			passed = true
		case "fail":
			failed = true
		default:
			pending = true
		}
	}
	if c.Operator == "all" && failed {
		return "fail"
	}
	if c.Operator == "any" && passed {
		return "pass"
	}
	if pending {
		return "pending"
	}
	if c.Operator == "all" {
		return "pass"
	}
	return "fail"
}

// Only matching rules take part in priority selection. Equal-priority group
// assignments merge into one replacement; a pause wins over a resume.
func EvaluateScheduledTestCombinations(config ScheduledTestProtectionConfig, verdicts map[int64]string) ScheduledTestCombinationDecision {
	decision := ScheduledTestCombinationDecision{}
	if !config.Enabled || !config.UsesCombinations() {
		return decision
	}
	priority := -1
	for _, rule := range config.Combinations {
		if rule.Priority < priority || EvaluateScheduledTestCombinationCondition(rule.Condition, verdicts) != "pass" {
			continue
		}
		if rule.Priority > priority {
			priority = rule.Priority
			decision = ScheduledTestCombinationDecision{Action: &ScheduledTestOutcomeAction{Scheduling: "keep", GroupMode: "keep"}}
		}
		decision.RuleIDs = append(decision.RuleIDs, rule.ID)
		if rule.Action.GroupMode == "assign" {
			decision.Action.GroupMode = "assign"
			decision.Action.GroupIDs = append(decision.Action.GroupIDs, rule.Action.GroupIDs...)
		}
		if rule.Action.Scheduling == "pause" || (rule.Action.Scheduling == "resume" && decision.Action.Scheduling != "pause") {
			decision.Action.Scheduling = rule.Action.Scheduling
		}
	}
	if decision.Action != nil {
		slices.Sort(decision.Action.GroupIDs)
		decision.Action.GroupIDs = slices.Compact(decision.Action.GroupIDs)
		slices.Sort(decision.RuleIDs)
	}
	return decision
}
