package service

import "slices"

// ExcludesAccount is scoped to this policy, never to the account's scheduler.
func (p *ScheduledTestPlan) ExcludesAccount(accountID int64) bool {
	return p != nil && slices.Contains(p.ExcludedAccountIDs, accountID)
}

func (p *ScheduledTestPlan) FilterExcludedAccounts(ids []int64) []int64 {
	if p == nil || len(p.ExcludedAccountIDs) == 0 {
		return ids
	}
	excluded := make(map[int64]bool, len(p.ExcludedAccountIDs))
	for _, id := range p.ExcludedAccountIDs {
		excluded[id] = true
	}
	filtered := make([]int64, 0, len(ids))
	for _, id := range ids {
		if !excluded[id] {
			filtered = append(filtered, id)
		}
	}
	return filtered
}
