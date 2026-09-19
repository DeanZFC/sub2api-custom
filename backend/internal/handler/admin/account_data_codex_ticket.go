package admin

import (
	"fmt"
	"maps"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
)

func codexTicketProxyIDsForExport(account *service.Account) []int64 {
	return service.ResolveOpenAICodexTicketAccountConfig(account, config.OpenAICodexTicketConfig{}).HarvestProxyIDs
}

// Proxy database IDs are local to an installation. Backups use the same portable
// proxy keys as business proxies, including an explicit empty list when omitted
// from the backup. Ticket credentials themselves are never exported.
func exportCodexTicketProxyExtra(account *service.Account, proxyKeyByID map[int64]string) (map[string]any, *[]string) {
	extra := service.RedactOpenAICodexTicketExtra(account.Extra)
	if _, configured := extra[service.OpenAICodexTicketHarvestProxyIDsExtraKey]; !configured {
		return extra, nil
	}
	keys := []string{}
	for _, id := range codexTicketProxyIDsForExport(account) {
		if key, ok := proxyKeyByID[id]; ok {
			keys = append(keys, key)
		}
	}
	delete(extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
	return extra, &keys
}

func importCodexTicketProxyExtra(extra map[string]any, keys *[]string, proxyKeyToID map[string]int64) (map[string]any, error) {
	if keys == nil {
		if _, hasLocalIDs := extra[service.OpenAICodexTicketHarvestProxyIDsExtraKey]; hasLocalIDs {
			return nil, fmt.Errorf("codex_ticket_proxy_keys is required when importing ticket proxies; source proxy IDs cannot be reused")
		}
		return extra, nil
	}
	ids := make([]int64, 0, len(*keys))
	for index, key := range *keys {
		id, ok := proxyKeyToID[key]
		if !ok || id <= 0 {
			return nil, fmt.Errorf("codex_ticket_proxy_keys[%d] not found", index)
		}
		ids = append(ids, id)
	}
	result := maps.Clone(extra)
	if result == nil {
		result = make(map[string]any)
	}
	result[service.OpenAICodexTicketHarvestProxyIDsExtraKey] = ids
	return result, nil
}
