package service

import (
	"maps"
	"strings"
)

// IsRetiredAccountExtraKey identifies obsolete credentials and configuration.
// Keep this boundary for old backups, stale caches and full-account writes;
// these keys no longer enable any gateway behavior.
func IsRetiredAccountExtraKey(key string) bool {
	return strings.HasPrefix(key, "codex_turn_ticket:") ||
		strings.HasPrefix(key, "codex_ticket_") ||
		key == "codex_turn_cookies" || key == "codex_harvest_proxy_url" ||
		key == "upstream_billing_rate_limit"
}

// StripRetiredAccountExtra returns a copy without obsolete account material.
// It must not mutate shared scheduler snapshots or the caller's input.
func StripRetiredAccountExtra(extra map[string]any) map[string]any {
	clean := maps.Clone(extra)
	for key := range clean {
		if IsRetiredAccountExtraKey(key) {
			delete(clean, key)
		}
	}
	if mode, _ := clean["codex_fingerprint_mode"].(string); strings.TrimSpace(mode) == "single_machine_multi_window" {
		clean["codex_fingerprint_mode"] = "off"
	}
	return clean
}
