package service

import (
	"sort"
	"strings"
)

// TestPickerModel is the compact catalog used by the account test dialog.
type TestPickerModel struct {
	ID          string `json:"id"`
	Type        string `json:"type"`
	DisplayName string `json:"display_name"`
}

// ConfiguredTestModelIDs returns concrete model IDs from the account allowlist
// or mapping. Empty means the account has no restriction and the caller should
// fall back to platform defaults. Wildcard mapping keys are skipped because
// they are not selectable test targets.
func ConfiguredTestModelIDs(account *Account) []string {
	if account == nil || account.IsOpenAIPassthroughEnabled() {
		return nil
	}
	mapping := account.GetModelMapping()
	if len(mapping) == 0 {
		return nil
	}
	ids := make([]string, 0, len(mapping))
	seen := make(map[string]struct{}, len(mapping))
	for id := range mapping {
		id = strings.TrimSpace(id)
		if id == "" || strings.Contains(id, "*") {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return ids
}

func TestPickerModels(ids []string) []TestPickerModel {
	out := make([]TestPickerModel, 0, len(ids))
	for _, id := range ids {
		out = append(out, TestPickerModel{ID: id, Type: "model", DisplayName: id})
	}
	return out
}
