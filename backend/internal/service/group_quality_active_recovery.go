package service

import "context"

// GroupQualityActiveRecoveryGate is checked before emergency routing. An
// active-recovery policy never lends a suspended account to customer traffic.
// Final database admission must enforce the same rule against live policies.
type GroupQualityActiveRecoveryGate interface {
	GroupQualityActiveRecoveryEnabled(context.Context, int64, string) (bool, error)
}

func groupQualityRequiresActiveRecovery(account *Account, model string) bool {
	if account == nil {
		return false
	}
	holds, _ := account.Extra["passive_quality"].([]any)
	for _, raw := range holds {
		hold, ok := raw.(map[string]any)
		if ok && hold["recovery_mode"] == "active" && (hold["scope"] == "account" || hold["model"] == model || model == "") {
			return true
		}
	}
	return false
}
