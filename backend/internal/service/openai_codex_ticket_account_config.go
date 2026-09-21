package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	OpenAICodexTicketEnabledExtraKey    = "codex_ticket_enabled"
	OpenAICodexTicketFailClosedExtraKey = "codex_ticket_fail_closed"
	// Retained only to discard settings from the retired per-account proxy pool.
	OpenAICodexTicketHarvestProxyIDsExtraKey = "codex_ticket_harvest_proxy_ids"
)

// OpenAICodexTicketAccountConfig exposes switches and the expected ticket length,
// never proxy credentials or captured ticket material. Enabled is the effective
// policy; AccountEnabled preserves the account choice while the gateway is off.
type OpenAICodexTicketAccountConfig struct {
	GatewayEnabled bool `json:"gateway_enabled"`
	AccountEnabled bool `json:"account_enabled"`
	Enabled        bool `json:"enabled"`
	FailClosed     bool `json:"fail_closed"`
	TargetLength   int  `json:"target_length"`
}

func ResolveOpenAICodexTicketAccountConfig(account *Account, fallback config.OpenAICodexTicketConfig) OpenAICodexTicketAccountConfig {
	policy := OpenAICodexTicketAccountConfig{
		GatewayEnabled: fallback.Enabled,
		FailClosed:     true,
		TargetLength:   openAICodexTicketTargetLength(account, fallback),
	}
	if !isOpenAICodexTicketAccount(account) {
		return policy
	}
	policy.AccountEnabled = true // Preserve untouched pre-account-switch installations.
	if raw, ok := account.Extra[OpenAICodexTicketEnabledExtraKey]; ok {
		policy.AccountEnabled, _ = raw.(bool)
	} else {
		policy.FailClosed = fallback.FailClosed
	}
	if value, ok := account.Extra[OpenAICodexTicketFailClosedExtraKey].(bool); ok {
		policy.FailClosed = value
	}
	policy.Enabled = policy.GatewayEnabled && policy.AccountEnabled
	return policy
}

// Length is an account-plan selection rule, not a claim about model quality.
// Known personal Pro plans use 292; Team/Business workspaces use 332. Keep the
// configured default for other/unknown plans instead of guessing from a ticket.
func openAICodexTicketTargetLength(account *Account, fallback config.OpenAICodexTicketConfig) int {
	if account != nil {
		plan := normalizedOpenAICodexTicketPlan(account)
		switch plan {
		case "team", "chatgptteam", "business", "chatgptbusiness":
			return 332
		case "pro", "chatgptpro", "prolite", "chatgptprolite":
			return 292
		}
		if strings.HasPrefix(plan, "selfservebusiness") {
			return 332
		}
	}
	if fallback.TargetLength > 0 {
		return fallback.TargetLength
	}
	return 292
}

func (s *OpenAIGatewayService) openAICodexTicketAccountConfig(ctx context.Context, account *Account) OpenAICodexTicketAccountConfig {
	cfg := s.openAICodexTicketConfig()
	cfg.Enabled = s.openAICodexTicketEnabledContext(ctx)
	return ResolveOpenAICodexTicketAccountConfig(account, cfg)
}

func normalizedOpenAICodexTicketPlan(account *Account) string {
	if account == nil {
		return ""
	}
	plan := strings.TrimSpace(account.GetCredential("plan_type"))
	if plan == "" {
		plan = account.GetCredential("chatgpt_plan_type")
	}
	return strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(strings.TrimSpace(plan)))
}
