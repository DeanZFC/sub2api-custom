package service

import (
	"context"
	"strings"
	"time"
)

// Recovery follows the production mapping order: the policy's public model
// first resolves inside its group/channel, then the existing platform adapter
// applies account mapping. The runner keeps the public model on its evidence.
func (s *AccountTestService) passiveRecoveryModel(ctx context.Context, account *Account, publicModel string, now time.Time) (string, *QualityDeferredError) {
	model := strings.TrimSpace(publicModel)
	recovery, _ := ctx.Value(passiveRecoveryRequestKey{}).(*passiveRecoveryRequest)
	if recovery == nil || recovery.config == nil || len(recovery.config.GroupIDs) == 0 {
		return model, nil
	}
	deferModel := func(reason string) (string, *QualityDeferredError) {
		return model, &QualityDeferredError{Reason: reason, Until: now.Add(time.Minute)}
	}
	if len(recovery.config.GroupIDs) != 1 {
		return deferModel("active_recovery_group_ambiguous")
	}
	// AccountTestService already receives this gateway in production for every
	// provider. Standalone adapters without channel configuration remain usable.
	if s.openaiGatewayService == nil || s.openaiGatewayService.channelService == nil {
		return model, nil
	}
	channels := s.openaiGatewayService.channelService
	cache, err := channels.loadCache(ctx)
	if err != nil || cache == nil || cache.loadFailed {
		return deferModel("active_recovery_channel_mapping_unavailable")
	}
	groupID := recovery.config.GroupIDs[0]
	channel := cache.channelByGroupID[groupID]
	if channel == nil || !channel.IsActive() {
		return model, nil
	}
	lookup := &channelLookup{cache: cache, channel: channel, platform: channelLookupPlatform(ctx, cache.groupPlatform[groupID])}
	// Use the same resolver as ResolveChannelMappingAndRestrict, keeping the
	// validated snapshot for restriction checks instead of swallowing DB errors.
	mapping := resolveMapping(lookup, groupID, model)
	mapped := strings.TrimSpace(mapping.MappedModel)
	if mapped == "" {
		return deferModel("active_recovery_channel_model_unavailable")
	}
	restrictedModel := billingModelForRestriction(mapping.BillingModelSource, model, mapped)
	if mapping.BillingModelSource == BillingModelSourceUpstream && account != nil {
		if account.IsOpenAI() {
			restrictedModel = resolveOpenAIAccountUpstreamModelForRequest(account, mapped, false)
		} else {
			restrictedModel = resolveAccountUpstreamModel(account, mapped)
		}
	}
	if restrictedModel != "" && checkRestricted(lookup, groupID, restrictedModel) {
		return deferModel("active_recovery_channel_model_restricted")
	}
	return mapped, nil
}
