package service

import (
	"context"
	"sort"
	"time"
)

// This projection is a routing preference, never an admission decision. The
// live group budget and the existing hard account checks remain authoritative.
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

// Split before TopK, subscription preference and weighted selection. Unknown
// and trial accounts remain in the fallback pool and still pass the live gate.
func groupQualityRoutingTiers(ctx context.Context, accounts []*Account, groupID *int64, requestedModel string) [][]*Account {
	model := passiveQualityRoutingModel(ctx, requestedModel)
	verified := make([]*Account, 0, len(accounts))
	fallback := make([]*Account, 0, len(accounts))
	for _, account := range accounts {
		if ready, _ := groupQualityRoutingState(account, groupID, model); ready {
			verified = append(verified, account)
		} else {
			fallback = append(fallback, account)
		}
	}
	if len(verified) == 0 || len(fallback) == 0 {
		return [][]*Account{accounts}
	}
	return [][]*Account{verified, fallback}
}

func groupQualityOrderAccounts(ctx context.Context, accounts []*Account, groupID *int64, model string) {
	tiers := groupQualityRoutingTiers(ctx, accounts, groupID, model)
	if len(tiers) == 2 {
		copy(accounts, append(tiers[0], tiers[1]...))
	}
}

func groupQualityOrderLoads(ctx context.Context, accounts []accountWithLoad, groupID *int64, requestedModel string) {
	model := passiveQualityRoutingModel(ctx, requestedModel)
	sort.SliceStable(accounts, func(i, j int) bool {
		a, _ := groupQualityRoutingState(accounts[i].account, groupID, model)
		b, _ := groupQualityRoutingState(accounts[j].account, groupID, model)
		return a && !b
	})
}
