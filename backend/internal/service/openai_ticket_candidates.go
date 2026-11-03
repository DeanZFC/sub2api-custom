package service

import (
	"context"
	"fmt"
)

// Ticket admission needs credentials and persisted tickets that sched:meta
// deliberately omits. Hydrate only gated candidates, before TopK filtering;
// looking solely at the metadata would reject even a valid warm-memory ticket
// because its identity cannot match the stripped credentials.
func (s *OpenAIGatewayService) listSchedulableAccountsForRequest(
	ctx context.Context, groupID *int64, platform, requestedModel string,
	requireCompact bool, excludedIDs map[int64]struct{},
) ([]Account, error) {
	accounts, err := s.listSchedulableAccounts(ctx, groupID, platform)
	if err != nil || s.schedulerSnapshot == nil || len(accounts) == 0 {
		return accounts, err
	}
	if !s.openAICodexTicketEnabledContext(ctx) || !s.openAICodexTicketConfig().FailClosed {
		return accounts, nil
	}
	ids := make([]int64, 0, len(accounts))
	hydrate := make(map[int64]struct{})
	for i := range accounts {
		account := &accounts[i]
		if _, excluded := excludedIDs[account.ID]; excluded {
			continue
		}
		if !account.IsSchedulable() || !isOpenAICodexTicketAccount(account) {
			continue
		}
		model := s.openAICodexTicketOutboundModel(account, requestedModel, requireCompact)
		if !isOpenAICodexTicketAccount(account, model) || !s.openAICodexTicketGatedModel(model) {
			continue
		}
		if _, exists := hydrate[account.ID]; !exists {
			ids = append(ids, account.ID)
			hydrate[account.ID] = struct{}{}
		}
	}
	if len(ids) == 0 {
		return accounts, nil
	}
	full, err := s.schedulerSnapshot.GetAccounts(ctx, ids)
	if err != nil {
		// Do not turn an unavailable authority into "no ticket", or fail open.
		return nil, fmt.Errorf("codex ticket scheduling state unavailable: %w", err)
	}
	result := make([]Account, 0, len(accounts))
	for i := range accounts {
		account := &accounts[i]
		if _, needed := hydrate[account.ID]; needed {
			account = full[account.ID]
			if account == nil || !s.openAIAccountMatchesSchedulingGroup(account, groupID) {
				continue
			}
		}
		result = append(result, *account)
	}
	return result, nil
}

// Compact capability ranking can use the original request flag=false while
// ticket gating must still use its actual compact model mapping.
func openAITicketCompactMode(requireCompact bool, override []bool) bool {
	if len(override) > 0 {
		return override[0]
	}
	return requireCompact
}

// Only fail-closed gated requests require tickets. Readiness is a ranking hint
// and must not exclude ordinary accounts when the feature is disabled.
func (s *OpenAIGatewayService) openAICodexTicketBlocksRequest(account *Account, requestedModel string, requireCompact bool) bool {
	return s.openAICodexTicketBlocksAccount(account, s.openAICodexTicketOutboundModel(account, requestedModel, requireCompact))
}

// A per-model Excel/BPS route takes precedence over an older native sticky
// affinity, while the existing policy, capability and transport gates still
// determine whether that alternate account can actually serve this request.
func (s *OpenAIGatewayService) shouldYieldStickyToExcelBPS(ctx context.Context, current *Account, groupID *int64, platform, model string, compact bool, excluded map[int64]struct{}, endpoint OpenAIEndpointCapability, image OpenAIImagesCapability, transport OpenAIUpstreamTransport) bool {
	if s == nil || current == nil || platform != PlatformOpenAI || model == "" || current.IsExcelBPSEnabledForModel(model) {
		return false
	}
	accounts, err := s.listSchedulableAccountsForRequest(ctx, groupID, platform, model, compact, excluded)
	if err != nil {
		return false
	}
	for i := range accounts {
		candidate := &accounts[i]
		if candidate.ID == current.ID || !candidate.IsExcelBPSEnabledForModel(model) {
			continue
		}
		if _, skip := excluded[candidate.ID]; skip {
			continue
		}
		if !isOpenAICompatibleAccountEligibleForRequest(ctx, candidate, platform, model, compact, endpoint) ||
			!accountSupportsOpenAICapabilitiesForRequest(candidate, model, endpoint, image) ||
			!s.isOpenAIAccountTransportCompatible(candidate, transport, model) ||
			!parentHealthyForShadow(candidate, s.parentAccountLookup(ctx)) ||
			s.isOpenAIAccountRequestRuntimeBlocked(candidate, model) {
			continue
		}
		if groupID != nil && s.needsUpstreamChannelRestrictionCheck(ctx, groupID) && s.isUpstreamModelRestrictedByChannel(ctx, *groupID, candidate, model, compact) {
			continue
		}
		return true
	}
	return false
}
