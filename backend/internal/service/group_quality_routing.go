package service

import "time"

// This projection identifies managed targets and fresh quality evidence; it
// does not change normal candidate priority or turn missing samples into a hold.
// The live recovery budget and hard account checks remain authoritative.
// Policies use the public request model, before provider model mapping.
func groupQualityRoutingState(account *Account, groupID *int64, model string) (verified, managed bool) {
	if account == nil || groupID == nil || *groupID <= 0 || model == "" {
		return false, false
	}
	entries, _ := account.Extra["group_quality_targets"].([]any)
	verified = true
	for _, raw := range entries {
		entry, ok := raw.(map[string]any)
		if !ok || int64(parseExtraInt(entry["group_id"])) != *groupID || entry["model"] != model {
			continue
		}
		managed = true
		verifiedUntil, _ := entry["verified_until"].(string)
		expiresAt, err := time.Parse(time.RFC3339, verifiedUntil)
		if entry["phase"] != "healthy" || entry["verified"] != true || err != nil || !expiresAt.After(time.Now()) {
			verified = false
		}
	}
	return verified && managed, managed
}
