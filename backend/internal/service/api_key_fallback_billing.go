package service

import (
	"context"
	"log/slog"
	"strings"
)

// resolveAPIKeyFallbackChannelUsageFields re-resolves channel attribution for
// the group that actually handled an API-key request. Handlers resolve their
// channel mapping before account selection (when the primary group is still
// active), so the fields captured by an asynchronous usage task can otherwise
// continue to point at the primary group's channel and model mapping.
//
// The helper is deliberately a no-op for ordinary requests. On a fallback
// request, channel metadata from the primary group must never be retained: a
// cache/repository error therefore clears the channel fields while retaining
// only the requested model identity. This is preferable to writing a
// confidently wrong primary-channel attribution into the usage row.
func resolveAPIKeyFallbackChannelUsageFields(
	ctx context.Context,
	channelService *ChannelService,
	apiKey *APIKey,
	fields ChannelUsageFields,
	requestedModel string,
	upstreamModel string,
) ChannelUsageFields {
	if !isAPIKeyFallbackActive(apiKey) || apiKey.GroupID == nil {
		return fields
	}
	groupID := *apiKey.GroupID
	if groupID <= 0 {
		return fields
	}

	// OriginalModel is the most reliable value because handlers populate it from
	// the client request. The remaining candidates keep internal/degraded paths
	// usable when a caller did not provide ChannelUsageFields.
	originalModel := firstNonEmptyTrimmed(
		fields.OriginalModel,
		requestedModel,
		fields.ChannelMappedModel,
		upstreamModel,
	)
	if originalModel == "" {
		return fields
	}
	// A fallback request must never retain channel metadata resolved for the
	// primary group. This also covers degraded/unit paths where the channel
	// service is not wired at all.
	withoutChannel := func() ChannelUsageFields {
		return ChannelUsageFields{
			OriginalModel:      originalModel,
			ChannelMappedModel: originalModel,
			BillingModelSource: BillingModelSourceRequested,
		}
	}
	if channelService == nil {
		return withoutChannel()
	}

	channel, err := channelService.GetChannelForGroup(ctx, groupID)
	if err != nil {
		slog.Warn("api_key_fallback_billing_channel_lookup_failed",
			"group_id", groupID,
			"api_key_id", apiKey.ID,
			"error", err)
		return withoutChannel()
	}
	if channel == nil {
		// No fallback channel means there is no valid primary channel metadata to
		// carry into the usage row. Keep only model identity; group-level pricing
		// still comes from apiKey.Group below the usage service.
		return withoutChannel()
	}

	mapping, _ := channelService.ResolveChannelMappingAndRestrict(ctx, &groupID, originalModel)
	return mapping.ToUsageFields(originalModel, upstreamModel)
}

func firstNonEmptyTrimmed(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

// fallbackAwareOpenAIBillingModel chooses a billing-model baseline from the
// fallback group's channel policy. The ForwardResult can still contain the
// primary channel's BillingModel because model mapping happens before account
// selection; that value is intentionally ignored for token billing here.
func fallbackAwareOpenAIBillingModel(result *OpenAIForwardResult, fields ChannelUsageFields) string {
	if result == nil {
		return firstNonEmptyTrimmed(fields.ChannelMappedModel, fields.OriginalModel)
	}
	requested := firstNonEmptyTrimmed(fields.OriginalModel, result.Model)
	upstream := strings.TrimSpace(result.UpstreamModel)
	mapped := firstNonEmptyTrimmed(fields.ChannelMappedModel, requested, upstream)

	switch strings.TrimSpace(fields.BillingModelSource) {
	case BillingModelSourceRequested:
		return firstNonEmptyTrimmed(requested, upstream, mapped)
	case BillingModelSourceChannelMapped:
		return firstNonEmptyTrimmed(mapped, requested, upstream)
	case BillingModelSourceUpstream, BillingModelSourceResponse:
		return firstNonEmptyTrimmed(upstream, mapped, requested)
	default:
		// No fallback channel (or an old channel snapshot without a source) must
		// prefer the actual upstream model, then the requested model. Never use
		// result.BillingModel as the baseline because it may belong to primary.
		return firstNonEmptyTrimmed(upstream, mapped, requested)
	}
}

func openAIResultHasMediaBilling(result *OpenAIForwardResult) bool {
	if result == nil {
		return false
	}
	return result.ImageCount > 0 || result.VideoCount > 0 ||
		result.WebSearchCalls > 0 || result.SearchCount > 0 || result.AudioUsage != nil
}
