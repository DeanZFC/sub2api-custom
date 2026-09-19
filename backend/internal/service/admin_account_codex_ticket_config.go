package service

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"math"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const maxOpenAICodexTicketHarvestProxyIDs = 64

func hasOpenAICodexTicketAccountPolicyKeys(extra map[string]any) bool {
	if extra == nil {
		return false
	}
	_, enabled := extra[OpenAICodexTicketEnabledExtraKey]
	_, failClosed := extra[OpenAICodexTicketFailClosedExtraKey]
	_, proxies := extra[OpenAICodexTicketHarvestProxyIDsExtraKey]
	return enabled || failClosed || proxies
}

// normalizeOpenAICodexTicketAccountExtra validates the account-owned 292
// policy and converts proxy IDs into a stable JSON representation. Ticket
// state itself is deliberately left untouched; MergeOpenAICodexTicketExtra
// remains responsible for protecting server-owned ticket material.
//
// When initialize is true (new account creation), all three policy fields are
// written explicitly. This prevents an account created after the migration
// from unexpectedly inheriting the legacy gateway switch.
func normalizeOpenAICodexTicketAccountExtra(platform string, extra map[string]any, initialize bool) (map[string]any, error) {
	normalized := maps.Clone(extra)
	if platform != PlatformOpenAI {
		return normalized, nil
	}
	if normalized == nil && !initialize {
		return nil, nil
	}
	if normalized == nil {
		normalized = make(map[string]any, 3)
	}

	if raw, ok := normalized[OpenAICodexTicketEnabledExtraKey]; ok {
		if _, valid := raw.(bool); !valid {
			return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketEnabledExtraKey, "must be a boolean")
		}
	}
	if raw, ok := normalized[OpenAICodexTicketFailClosedExtraKey]; ok {
		if _, valid := raw.(bool); !valid {
			return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketFailClosedExtraKey, "must be a boolean")
		}
	}
	if raw, ok := normalized[OpenAICodexTicketHarvestProxyIDsExtraKey]; ok {
		ids, err := normalizeOpenAICodexTicketHarvestProxyIDs(raw)
		if err != nil {
			return nil, err
		}
		normalized[OpenAICodexTicketHarvestProxyIDsExtraKey] = ids
	}
	if initialize {
		if _, ok := normalized[OpenAICodexTicketEnabledExtraKey]; !ok {
			normalized[OpenAICodexTicketEnabledExtraKey] = false
		}
		if _, ok := normalized[OpenAICodexTicketFailClosedExtraKey]; !ok {
			normalized[OpenAICodexTicketFailClosedExtraKey] = true
		}
		if _, ok := normalized[OpenAICodexTicketHarvestProxyIDsExtraKey]; !ok {
			normalized[OpenAICodexTicketHarvestProxyIDsExtraKey] = []int64{}
		}
	}
	return normalized, nil
}

// normalizeOpenAICodexTicketAccountUpdateExtra preserves policy keys omitted
// by a full account edit. An old account with no policy keys remains old and
// therefore continues to use the legacy gateway fallback until explicitly
// configured.
func normalizeOpenAICodexTicketAccountUpdateExtra(account *Account, extra map[string]any) (map[string]any, error) {
	if account == nil {
		return extra, nil
	}
	normalized, err := normalizeOpenAICodexTicketAccountExtra(account.Platform, extra, false)
	if err != nil || account.Platform != PlatformOpenAI {
		return normalized, err
	}
	if normalized == nil {
		normalized = make(map[string]any)
	}
	for _, key := range []string{
		OpenAICodexTicketEnabledExtraKey,
		OpenAICodexTicketFailClosedExtraKey,
		OpenAICodexTicketHarvestProxyIDsExtraKey,
	} {
		if _, provided := extra[key]; provided {
			continue
		}
		if value, exists := account.Extra[key]; exists {
			normalized[key] = value
		}
	}
	return normalized, nil
}

func invalidOpenAICodexTicketExtra(key, detail string) error {
	return infraerrors.BadRequest("INVALID_OPENAI_CODEX_TICKET_CONFIG", fmt.Sprintf("%s %s", key, detail))
}

func normalizeOpenAICodexTicketHarvestProxyIDs(raw any) ([]int64, error) {
	if raw == nil {
		return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, "must be an array of positive proxy IDs")
	}
	var values []any
	switch typed := raw.(type) {
	case []any:
		values = typed
	case []int64:
		values = make([]any, len(typed))
		for i, value := range typed {
			values[i] = value
		}
	case []int:
		values = make([]any, len(typed))
		for i, value := range typed {
			values[i] = value
		}
	case []float64:
		values = make([]any, len(typed))
		for i, value := range typed {
			values[i] = value
		}
	case json.Number:
		return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, "must be an array of positive proxy IDs")
	default:
		return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, "must be an array of positive proxy IDs")
	}
	if len(values) > maxOpenAICodexTicketHarvestProxyIDs {
		return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, fmt.Sprintf("must contain at most %d IDs", maxOpenAICodexTicketHarvestProxyIDs))
	}
	ids := make([]int64, 0, len(values))
	seen := make(map[int64]struct{}, len(values))
	for _, value := range values {
		id, ok := codexTicketProxyID(value)
		if !ok || id <= 0 {
			return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, "must contain only positive integer IDs")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids, nil
}

func codexTicketProxyID(value any) (int64, bool) {
	switch typed := value.(type) {
	case int:
		return int64(typed), true
	case int64:
		return typed, true
	case int32:
		return int64(typed), true
	case uint:
		if uint64(typed) > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case uint64:
		if typed > math.MaxInt64 {
			return 0, false
		}
		return int64(typed), true
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || typed != math.Trunc(typed) || typed >= math.Exp2(63) || typed < math.MinInt64 {
			return 0, false
		}
		return int64(typed), true
	case json.Number:
		id, err := typed.Int64()
		return id, err == nil
	default:
		return 0, false
	}
}

// validateOpenAICodexTicketHarvestProxyIDs verifies existence without
// applying business-proxy active/expiry rules. A dedicated 292 proxy may be
// retained while paused or expired and should remain selectable for later use.
func (s *adminServiceImpl) validateOpenAICodexTicketHarvestProxyIDs(ctx context.Context, ids []int64) ([]int64, error) {
	if len(ids) == 0 {
		return []int64{}, nil
	}
	if s == nil || s.proxyRepo == nil {
		return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, "proxy repository is unavailable")
	}
	validated := make([]int64, 0, len(ids))
	seen := make(map[int64]struct{}, len(ids))
	for _, id := range ids {
		if id <= 0 {
			return nil, invalidOpenAICodexTicketExtra(OpenAICodexTicketHarvestProxyIDsExtraKey, "must contain only positive integer IDs")
		}
		if _, exists := seen[id]; exists {
			continue
		}
		if proxy, err := s.proxyRepo.GetByID(ctx, id); err != nil || proxy == nil {
			if err != nil {
				return nil, infraerrors.BadRequest("INVALID_OPENAI_CODEX_TICKET_PROXY", fmt.Sprintf("proxy %d could not be found", id))
			}
			return nil, infraerrors.BadRequest("INVALID_OPENAI_CODEX_TICKET_PROXY", fmt.Sprintf("proxy %d could not be found", id))
		}
		seen[id] = struct{}{}
		validated = append(validated, id)
	}
	return validated, nil
}
