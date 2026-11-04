package service

import (
	"context"
	"strings"
)

// IsExcelBPSEnabled routes an existing ChatGPT OAuth account to the Excel gateway.
// The independent Excel grant must be ready before the switch changes routing.
func (a *Account) IsExcelBPSEnabled() bool {
	if !ExcelBPSAuthorizationEligible(a) {
		return false
	}
	enabled, _ := a.Extra["openai_excel_bps"].(bool)
	return enabled && a.Extra[ExcelBPSAuthorizationPendingKey] != true
}

const ExcelBPSIgnoreImagesKey = "openai_excel_bps_ignore_images"

// IsExcelBPSIgnoreImagesEnabled opts into text-only forwarding when global BPS
// image support is disabled. The forwarding path checks that global setting.
func (a *Account) IsExcelBPSIgnoreImagesEnabled() bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	enabled, _ := a.Extra[ExcelBPSIgnoreImagesKey].(bool)
	return enabled
}

const ExcelBPSIgnoreEncryptedContentKey = "openai_excel_bps_ignore_encrypted_content"

// IsExcelBPSIgnoreEncryptedContentEnabled opts into replacing ciphertext that
// BPS cannot forward, such as sub-agent messages in an old Codex conversation,
// with an omission notice instead of rejecting the whole request.
func (a *Account) IsExcelBPSIgnoreEncryptedContentEnabled() bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	enabled, _ := a.Extra[ExcelBPSIgnoreEncryptedContentKey].(bool)
	return enabled
}

func (a *Account) IsExcelBPSMihomoEnabled() bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	enabled, _ := a.Extra["openai_excel_bps_mihomo"].(bool)
	return enabled
}

// ExcelBPSProxySourceKey selects the pool behind the BPS session proxy toggle:
// the managed Mihomo kernel (default) or the admin proxy list (IP 管理).
const ExcelBPSProxySourceKey = "openai_excel_bps_proxy_source"

const (
	ExcelBPSProxySourceMihomo = "mihomo"
	ExcelBPSProxySourceIPPool = "ip_pool"
)

// ExcelBPSProxySource is meaningful only while the session proxy is enabled;
// unknown stored values fall back to the Mihomo pool rather than direct.
func (a *Account) ExcelBPSProxySource() string {
	if !a.IsExcelBPSMihomoEnabled() {
		return ""
	}
	source, _ := a.Extra[ExcelBPSProxySourceKey].(string)
	if source == ExcelBPSProxySourceIPPool {
		return ExcelBPSProxySourceIPPool
	}
	return ExcelBPSProxySourceMihomo
}

// IsExcelBPSCacheCreationAsInputEnabled controls local billing and downstream usage.
// The setting has no effect unless this account uses the Excel/BPS protocol.
func (a *Account) IsExcelBPSCacheCreationAsInputEnabled() bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	enabled, _ := a.Extra["openai_excel_bps_cache_creation_as_input"].(bool)
	return enabled
}

// IsExcelBPSAutoDisableOn403Enabled opts into disabling BPS after a generic 403.
func (a *Account) IsExcelBPSAutoDisableOn403Enabled() bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	enabled, _ := a.Extra["openai_excel_bps_auto_disable_on_403"].(bool)
	return enabled
}

// isExcelBPSAllModelsEnabled preserves legacy account-wide routing. An explicit
// list, including an empty or malformed list, never enables BPS for all models.
func (a *Account) isExcelBPSAllModelsEnabled() bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	_, scoped := a.Extra["openai_excel_bps_models"]
	return !scoped
}

// IsExcelBPSEnabledForModel selects the protocol after account model mapping.
// The list selects a protocol; it does not restrict access to other models.
func (a *Account) IsExcelBPSEnabledForModel(requestedModel string) bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	return a.isExcelBPSUpstreamModelEnabled(a.GetMappedModel(requestedModel))
}

func (a *Account) isExcelBPSUpstreamModelEnabled(model string) bool {
	if !a.IsExcelBPSEnabled() {
		return false
	}
	raw, scoped := a.Extra["openai_excel_bps_models"]
	if !scoped {
		return true
	}
	model = strings.TrimSpace(model)
	if model == "" {
		return false
	}
	switch models := raw.(type) {
	case []string:
		for _, selected := range models {
			if strings.TrimSpace(selected) == model {
				return true
			}
		}
	case []any:
		for _, selected := range models {
			if name, ok := selected.(string); ok && strings.TrimSpace(name) == model {
				return true
			}
		}
	}
	return false
}

// AccountExcelBPSRepository disables only BPS, provided the account credentials
// and both opt-in switches still match at the time of the write.
type AccountExcelBPSRepository interface {
	DisableExcelBPSOn403(ctx context.Context, account *Account) (bool, error)
}

// AccountExcelBPSGroupRepository applies an opted-in group action atomically
// after rechecking the account identity, policy and current memberships.
type AccountExcelBPSGroupRepository interface {
	MoveExcelBPSOn403(ctx context.Context, account *Account) (bool, error)
}

// SanitizeExcelBPSAuthorizationExtra removes client-supplied authorization
// metadata. The service derives pending from the persisted grant; only grant
// transactions may write credential diagnostics.
func SanitizeExcelBPSAuthorizationExtra(extra map[string]any) map[string]any {
	if extra == nil {
		return nil
	}
	clean := make(map[string]any, len(extra))
	for key, value := range extra {
		if key != ExcelBPSAuthorizationPendingKey && key != ExcelBPSCredentialStateExtraKey {
			clean[key] = value
		}
	}
	return clean
}
