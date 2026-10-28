package service

import (
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"
)

const ScheduledTestMaxModels = 16

// SelectedModelIDs keeps old single-model policies readable. ModelID is the
// execution anchor; callers creating individual executions replace it without
// changing the policy's complete selection.
func (p *ScheduledTestPlan) SelectedModelIDs() []string {
	if p == nil {
		return nil
	}
	if len(p.ModelIDs) > 0 {
		return p.ModelIDs
	}
	if p.ModelID != "" {
		return []string{p.ModelID}
	}
	return nil
}

func (p *ScheduledTestPlan) SelectsModel(model string) bool {
	return slices.Contains(p.SelectedModelIDs(), model)
}

func normalizeScheduledTestModelIDs(plan *ScheduledTestPlan) error {
	if plan.ModelIDs != nil && len(plan.ModelIDs) == 0 {
		return fmt.Errorf("model_ids requires at least one model")
	}
	models := plan.SelectedModelIDs()
	if len(models) == 0 || len(models) > ScheduledTestMaxModels {
		return fmt.Errorf("select between 1 and %d test models", ScheduledTestMaxModels)
	}
	selected := make([]string, 0, len(models))
	for _, model := range models {
		model = strings.TrimSpace(model)
		if model == "" || utf8.RuneCountInString(model) > 100 {
			return fmt.Errorf("model_ids must contain nonempty model IDs of at most 100 characters")
		}
		if !slices.Contains(selected, model) {
			selected = append(selected, model)
		}
	}
	plan.ModelIDs = selected
	plan.ModelID = selected[0]
	return nil
}
