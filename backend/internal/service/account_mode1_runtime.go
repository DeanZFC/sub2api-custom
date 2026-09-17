package service

import (
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const mode1PolicyVersion = 3

func isOpenAIOAuthLike(a *Account) bool    { return a != nil && a.IsOpenAIOAuthLike() }
func isAnthropicOAuthLike(a *Account) bool { return a != nil && a.IsAnthropicOAuthOrSetupToken() }

func mode1Marker(a *Account) map[string]any {
	if a == nil || a.Extra == nil {
		return nil
	}
	m, _ := a.Extra[AntiDegradeMarkerExtraKey].(map[string]any)
	return m
}
func mode1Int(v any) int {
	switch n := v.(type) {
	case int:
		return n
	case int64:
		return int(n)
	case int32:
		return int(n)
	case float64:
		if n == float64(int(n)) {
			return int(n)
		}
	case float32:
		if n == float32(int(n)) {
			return int(n)
		}
	}
	return 0
}
func isMode1ProtectionEnabled(a *Account) bool {
	m := mode1Marker(a)
	if m == nil || !a.AntiDegradationEnabled() || m["enabled"] != true || m["mode"] != string(AntiDegradeMode1) {
		return false
	}
	version := mode1Int(m["policy_version"])
	return version == 2 || version == mode1PolicyVersion
}
func isMode1V2ProtectionEnabled(a *Account) bool {
	return isMode1ProtectionEnabled(a) && mode1Int(mode1Marker(a)["policy_version"]) == 2
}

func antiDegradeAccountTLSProfile(a *Account) string {
	if isMode1V2ProtectionEnabled(a) {
		// Version 2 used this explicit flag to choose Node.js 24. Version 3
		// moved to standard transport; reading v2 must not change its wire path.
		if a.Extra["enable_tls_fingerprint"] == true {
			return "nodejs24"
		}
		return "standard"
	}
	return antiDegradeStrategyProfile(antiDegradeMode(a)).TLSProfile
}
func isLegacyProtectionEnabled(a *Account) bool {
	m := mode1Marker(a)
	return m != nil && m["enabled"] == true && m["mode"] == string(AntiDegradeModeLegacy)
}
func isMode1ProtectionRequested(a *Account) bool {
	m := mode1Marker(a)
	if m == nil {
		return false
	}
	_, versionPresent := m["policy_version"]
	return m["enabled"] != false && m["mode"] != string(AntiDegradeMode2) && versionPresent
}
func hasMode1ManagedUpdates(updates map[string]any) bool {
	for _, key := range mode1ManagedExtraKeys {
		if _, ok := updates[key]; ok {
			return true
		}
	}
	return false
}
func (a *Account) IsMode1ProtectionEnabled() bool { return isMode1ProtectionEnabled(a) }
func (a *Account) Mode1EffectiveConcurrency() int {
	if a == nil {
		return 0
	}
	if !a.AntiDegradationEnabled() || a.Concurrency > 0 {
		return a.Concurrency
	}
	return AntiDegradeConcurrencyCap
}

var mode1ManagedExtraKeys = []string{AntiDegradeMarkerExtraKey, codexFingerprintModeExtraKey, "enable_tls_fingerprint", "tls_fingerprint_builtin", "tls_fingerprint_profile_id", "proxy_mode"}

// validateRegisteredAntiDegrade checks persisted strategy fields at runtime.
// It is intentionally conservative: malformed protected state fails closed,
// while unprotected accounts keep the old path.
func validateRegisteredAntiDegrade(a *Account) error {
	if a == nil || !a.AntiDegradationEnabled() {
		return nil
	}
	mode := antiDegradeMode(a)
	p := antiDegradeStrategyProfile(mode)
	if p.ID == "" || !p.ApplySupported {
		return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", "未知的账号保护策略")
	}
	if reason := antiDegradeEligibilityIssue(a, mode); reason != "" {
		return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", reason)
	}
	if m := mode1Marker(a); m != nil && mode1Int(m["max_concurrency"]) < 1 {
		return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", "保护并发上限无效")
	}
	if mode == AntiDegradeModeGeneric {
		return nil
	}
	if mode == AntiDegradeMode1 && !isMode1ProtectionEnabled(a) {
		return infraerrors.BadRequest("MODE1_CONFIGURATION_INVALID", "账号保护配置不完整，请通过专用保护入口修复")
	}
	if isOpenAIOAuthLike(a) {
		if a.GetCodexFingerprintMode() != codexFingerprintMode(p.IdentityMode) {
			return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", "保护身份配置与已选策略不一致")
		}
		if _, ok := codexFingerprintSeed(a.Extra); !ok {
			return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", "账号持久身份种子缺失或无效")
		}
	}
	if isMode1V2ProtectionEnabled(a) {
		// Historical v2 rows use the boolean to select a fixed Node.js 24
		// profile, ignoring stored template fields. Validate their identity and
		// policy above but retain that established transport behavior.
		return nil
	}
	if p.TLSProfile == "standard" || p.TLSProfile == "mac_codex" {
		if a.Extra["enable_tls_fingerprint"] == true || a.Extra["tls_fingerprint_builtin"] != nil || a.Extra["tls_fingerprint_profile_id"] != nil {
			return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", "传输配置与已选策略不一致")
		}
	} else if a.Extra["enable_tls_fingerprint"] != true || a.Extra["tls_fingerprint_builtin"] != p.TLSProfile || a.Extra["tls_fingerprint_profile_id"] != nil {
		return infraerrors.BadRequest("PROTECTION_CONFIGURATION_INVALID", "TLS 配置与已选策略不一致")
	}
	return nil
}
