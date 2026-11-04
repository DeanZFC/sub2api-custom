package config

import "fmt"

// ResolveAstraDependencies returns detached source and target selections for stored SSE tickets.
func ResolveAstraDependencies(value AstraRoutingSettings) (AstraRoutingSettings, error) {
	if value.CookiePool.RotateNodes {
		value.CookiePool.IPAffinity = true
	}
	value.SchedulingGroupIDs = append([]int64(nil), value.SchedulingGroupIDs...)
	value.CookiePool.SourceAccountIDs = append([]int64(nil), value.CookiePool.SourceAccountIDs...)
	value.CookiePool.TargetAccountIDs = append([]int64(nil), value.CookiePool.TargetAccountIDs...)
	value.WSSession.AccountIDs = append([]int64(nil), value.WSSession.AccountIDs...)
	if value.CookiePool.Enabled && len(value.CookiePool.SourceAccountIDs) == 0 {
		return value, fmt.Errorf("astra_source_required")
	}
	return value, value.Validate()
}
