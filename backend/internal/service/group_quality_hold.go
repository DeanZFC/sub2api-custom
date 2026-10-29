package service

import (
	"context"
	"errors"
)

// ErrAccountQualityHeld indicates temporary automatic quality protection. It
// preserves continuation ownership without allowing customer recovery traffic.
var ErrAccountQualityHeld = errors.New("account is paused by quality protection pending active checks")

// groupQualityAutomaticHold preserves continuation ownership during automatic
// protection. Detection never authorizes a request-local scheduling bypass.
func groupQualityAutomaticHold(ctx context.Context, account *Account, groupID *int64, requestedModel string) bool {
	if account == nil || groupID == nil || *groupID <= 0 || !account.IsOpenAI() {
		return false
	}
	model := passiveQualityRoutingModel(ctx, requestedModel)
	groupTrial := false
	holds, _ := account.Extra["passive_quality"].([]any)
	for _, raw := range holds {
		hold, ok := raw.(map[string]any)
		if !ok || (hold["purpose"] != "group_quality_target" && hold["purpose"] != "cache_target") {
			continue
		}
		if hold["phase"] == "trial" && (hold["scope"] == "account" || hold["model"] == model) {
			groupTrial = true
		}
	}
	return account.Status == "quality_paused" || groupTrial || account.PassiveQualityBlocked(model) || account.ActiveTestModelBlocked(model)
}
