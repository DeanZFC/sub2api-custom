package service

import (
	"context"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
)

const (
	OpenAICodexTicketEnabledExtraKey         = "codex_ticket_enabled"
	OpenAICodexTicketFailClosedExtraKey      = "codex_ticket_fail_closed"
	OpenAICodexTicketHarvestProxyIDsExtraKey = "codex_ticket_harvest_proxy_ids"
)

// OpenAICodexTicketAccountConfig is the effective account policy exposed to
// administrators. Proxy credentials and captured ticket material are not included.
type OpenAICodexTicketAccountConfig struct {
	Enabled             bool    `json:"enabled"`
	FailClosed          bool    `json:"fail_closed"`
	HarvestProxyIDs     []int64 `json:"harvest_proxy_ids"`
	LegacyProxyFallback bool    `json:"legacy_proxy_fallback"`
}

// ResolveOpenAICodexTicketAccountConfig keeps old accounts working until their
// policy is saved. Explicit account values always take precedence over the old
// gateway switch. Missing fail_closed defaults to protecting gated models.
func ResolveOpenAICodexTicketAccountConfig(account *Account, fallback config.OpenAICodexTicketConfig) OpenAICodexTicketAccountConfig {
	policy := OpenAICodexTicketAccountConfig{
		Enabled:             fallback.Enabled,
		FailClosed:          true,
		HarvestProxyIDs:     []int64{},
		LegacyProxyFallback: true,
	}
	if !isOpenAICodexTicketAccount(account) {
		policy.Enabled = false
		policy.LegacyProxyFallback = false
		return policy
	}
	if raw, ok := account.Extra[OpenAICodexTicketEnabledExtraKey]; ok {
		policy.Enabled, _ = raw.(bool)
	} else {
		// Untouched legacy accounts keep their existing gateway policy. New
		// account policies default to fail-closed as soon as enabled is explicit.
		policy.FailClosed = fallback.FailClosed
	}
	if value, ok := account.Extra[OpenAICodexTicketFailClosedExtraKey].(bool); ok {
		policy.FailClosed = value
	}
	if raw, ok := account.Extra[OpenAICodexTicketHarvestProxyIDsExtraKey]; ok {
		policy.LegacyProxyFallback = false
		policy.HarvestProxyIDs = openAICodexTicketProxyIDs(raw)
	}
	return policy
}

func openAICodexTicketProxyIDs(raw any) []int64 {
	ids := []int64{}
	seen := map[int64]bool{}
	appendID := func(id int64) {
		if id > 0 && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	switch values := raw.(type) {
	case []int64:
		for _, id := range values {
			appendID(id)
		}
	case []int:
		for _, id := range values {
			appendID(int64(id))
		}
	case []any:
		for _, value := range values {
			id, ok := toInt64(value)
			if decimal, isFloat := value.(float64); isFloat && float64(id) != decimal {
				continue
			}
			if ok {
				appendID(id)
			}
		}
	}
	return ids
}

func (s *OpenAIGatewayService) openAICodexTicketAccountConfig(ctx context.Context, account *Account) OpenAICodexTicketAccountConfig {
	cfg := s.openAICodexTicketConfig()
	if account != nil {
		if _, configured := account.Extra[OpenAICodexTicketEnabledExtraKey]; !configured {
			cfg.Enabled = s.openAICodexTicketEnabledContext(ctx)
		}
	}
	return ResolveOpenAICodexTicketAccountConfig(account, cfg)
}

type openAICodexTicketHarvestProxy struct {
	id  int64
	url string
}

func (s *OpenAIGatewayService) openAICodexTicketHarvestProxies(ctx context.Context, policy OpenAICodexTicketAccountConfig) []openAICodexTicketHarvestProxy {
	if policy.LegacyProxyFallback {
		proxyURL := s.openAICodexTicketHarvestProxyURLContext(ctx)
		if proxyURL != "" && ValidateOpenAICodexTicketHarvestProxyURL(proxyURL) == nil {
			return []openAICodexTicketHarvestProxy{{url: proxyURL}}
		}
		return nil
	}
	if len(policy.HarvestProxyIDs) == 0 || s.codexTicketProxyRepo == nil {
		return nil
	}
	proxies, err := s.codexTicketProxyRepo.ListByIDs(ctx, policy.HarvestProxyIDs)
	if err != nil {
		return nil
	}
	byID := make(map[int64]Proxy, len(proxies))
	for _, proxy := range proxies {
		byID[proxy.ID] = proxy
	}
	available := make([]openAICodexTicketHarvestProxy, 0, len(proxies))
	now := time.Now()
	for _, id := range policy.HarvestProxyIDs {
		proxy, ok := byID[id]
		if !ok || !proxy.IsActive() || proxy.IsExpired(now) {
			continue
		}
		proxyURL := proxy.URL()
		if ValidateOpenAICodexTicketHarvestProxyURL(proxyURL) != nil {
			continue
		}
		available = append(available, openAICodexTicketHarvestProxy{id: id, url: proxyURL})
	}
	return available
}
