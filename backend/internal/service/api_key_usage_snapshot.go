package service

import (
	"context"
)

// apiKeyForUsageContextKey carries the request-local API key to the usage-task
// submitter.  The middleware stores the authenticated pointer here; the
// submitter immediately turns it into an immutable snapshot before handing the
// task to the worker pool.
type apiKeyForUsageContextKey struct{}

// apiKeyUsageSnapshotContextKey carries the immutable API-key snapshot through
// the detached worker context.  It is deliberately private so callers cannot
// accidentally mutate the value without going through the clone helper.
type apiKeyUsageSnapshotContextKey struct{}

// WithAPIKeyForUsageContext associates the request-local API key with ctx.
// This is separate from the Gin context value because usage tasks run after the
// handler returns and must not retain a Gin context.
func WithAPIKeyForUsageContext(ctx context.Context, apiKey *APIKey) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, apiKeyForUsageContextKey{}, apiKey)
}

// APIKeyForUsageContext returns the authenticated API-key pointer associated
// with ctx, if one was installed by the API-key middleware.
func APIKeyForUsageContext(ctx context.Context) (*APIKey, bool) {
	if ctx == nil {
		return nil, false
	}
	apiKey, ok := ctx.Value(apiKeyForUsageContextKey{}).(*APIKey)
	return apiKey, ok && apiKey != nil
}

// WithAPIKeyUsageSnapshot stores a defensive copy of apiKey in ctx.  The copy
// is taken at task submission time, before another turn/request can switch the
// mutable request API key to a fallback group.
func WithAPIKeyUsageSnapshot(ctx context.Context, apiKey *APIKey) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, apiKeyUsageSnapshotContextKey{}, CloneAPIKeyForUsage(apiKey))
}

// APIKeyUsageSnapshotFromContext returns the captured usage snapshot.
func APIKeyUsageSnapshotFromContext(ctx context.Context) (*APIKey, bool) {
	if ctx == nil {
		return nil, false
	}
	apiKey, ok := ctx.Value(apiKeyUsageSnapshotContextKey{}).(*APIKey)
	return apiKey, ok && apiKey != nil
}

// CloneAPIKeyForUsage creates an immutable, request-scoped copy containing all
// fields that billing, quota, pricing, and notification code may inspect.  In
// particular, GroupID and nested Group/User pointers are copied so fallback
// activation in a later request cannot rewrite an already queued usage task.
func CloneAPIKeyForUsage(src *APIKey) *APIKey {
	if src == nil {
		return nil
	}
	clone := *src
	clone.GroupID = cloneUsagePointer(src.GroupID)
	clone.FallbackGroupID = cloneUsagePointer(src.FallbackGroupID)
	clone.LastUsedAt = cloneUsagePointer(src.LastUsedAt)
	clone.LastUsedIP = cloneUsagePointer(src.LastUsedIP)
	clone.CreatedAt = src.CreatedAt
	clone.UpdatedAt = src.UpdatedAt
	clone.ExpiresAt = cloneUsagePointer(src.ExpiresAt)
	clone.IPWhitelist = append([]string(nil), src.IPWhitelist...)
	clone.IPBlacklist = append([]string(nil), src.IPBlacklist...)
	clone.User = cloneUsageUserForUsage(src.User)
	clone.Group = cloneUsageGroup(src.Group)
	clone.FallbackGroup = cloneUsageGroup(src.FallbackGroup)
	// These compiled rules are immutable after authentication and can be shared;
	// copying the pointer avoids retaining another parser/cache object.
	return &clone
}

func cloneUsagePointer[T any](src *T) *T {
	if src == nil {
		return nil
	}
	clone := *src
	return &clone
}

func cloneUsageUserForUsage(src *User) *User {
	if src == nil {
		return nil
	}
	clone := *src
	clone.AllowedGroups = append([]int64(nil), src.AllowedGroups...)
	clone.GroupRates = make(map[int64]float64, len(src.GroupRates))
	for groupID, rate := range src.GroupRates {
		clone.GroupRates[groupID] = rate
	}
	clone.UserGroupRPMOverride = cloneUsagePointer(src.UserGroupRPMOverride)
	clone.BalanceNotifyThreshold = cloneUsagePointer(src.BalanceNotifyThreshold)
	clone.TotpSecretEncrypted = cloneUsagePointer(src.TotpSecretEncrypted)
	clone.LastLoginAt = cloneUsagePointer(src.LastLoginAt)
	clone.LastActiveAt = cloneUsagePointer(src.LastActiveAt)
	clone.LastUsedAt = cloneUsagePointer(src.LastUsedAt)
	clone.CreatedAt = src.CreatedAt
	clone.UpdatedAt = src.UpdatedAt
	clone.DeletedAt = cloneUsagePointer(src.DeletedAt)
	clone.TotpEnabledAt = cloneUsagePointer(src.TotpEnabledAt)
	clone.BalanceNotifyExtraEmails = append([]NotifyEmailEntry(nil), src.BalanceNotifyExtraEmails...)
	// APIKeys and Subscriptions are not needed by usage billing.  Leaving them
	// nil also prevents a queued task from retaining the entire user graph.
	clone.APIKeys = nil
	clone.Subscriptions = nil
	return &clone
}

func cloneUsageGroup(src *Group) *Group {
	if src == nil {
		return nil
	}
	clone := *src
	clone.DailyLimitUSD = cloneUsagePointer(src.DailyLimitUSD)
	clone.WeeklyLimitUSD = cloneUsagePointer(src.WeeklyLimitUSD)
	clone.MonthlyLimitUSD = cloneUsagePointer(src.MonthlyLimitUSD)
	clone.ImagePrice1K = cloneUsagePointer(src.ImagePrice1K)
	clone.ImagePrice2K = cloneUsagePointer(src.ImagePrice2K)
	clone.ImagePrice4K = cloneUsagePointer(src.ImagePrice4K)
	clone.VideoPrice480P = cloneUsagePointer(src.VideoPrice480P)
	clone.VideoPrice720P = cloneUsagePointer(src.VideoPrice720P)
	clone.VideoPrice1080P = cloneUsagePointer(src.VideoPrice1080P)
	clone.WebSearchPricePerCall = cloneUsagePointer(src.WebSearchPricePerCall)
	clone.SearchPricePer1k = cloneUsagePointer(src.SearchPricePer1k)
	clone.AudioRealtimePricePerMin = cloneUsagePointer(src.AudioRealtimePricePerMin)
	clone.AudioTTSPricePerMillionChars = cloneUsagePointer(src.AudioTTSPricePerMillionChars)
	clone.AudioSTTPricePerHour = cloneUsagePointer(src.AudioSTTPricePerHour)
	clone.FallbackGroupID = cloneUsagePointer(src.FallbackGroupID)
	clone.FallbackGroupIDOnInvalidRequest = cloneUsagePointer(src.FallbackGroupIDOnInvalidRequest)
	clone.VideoModelPrices = cloneUsageVideoModelPrices(src.VideoModelPrices)
	clone.ModelRouting = cloneUsageModelRouting(src.ModelRouting)
	clone.SupportedModelScopes = append([]string(nil), src.SupportedModelScopes...)
	clone.ReasoningEffortMappings = append([]ReasoningEffortMapping(nil), src.ReasoningEffortMappings...)
	clone.MessagesDispatchModelConfig = cloneGroupMessagesDispatchModelConfig(src.MessagesDispatchModelConfig)
	clone.ModelsListConfig.Models = append([]string(nil), src.ModelsListConfig.Models...)
	if src.ModelPricing != nil {
		clone.ModelPricing = make([]ChannelModelPricing, len(src.ModelPricing))
		for i := range src.ModelPricing {
			clone.ModelPricing[i] = cloneUsageChannelModelPricing(src.ModelPricing[i])
		}
	}
	if src.AccountGroups != nil {
		clone.AccountGroups = append([]AccountGroup(nil), src.AccountGroups...)
	}
	return &clone
}

func cloneUsageVideoModelPrices(src map[string]map[string]float64) map[string]map[string]float64 {
	if src == nil {
		return nil
	}
	clone := make(map[string]map[string]float64, len(src))
	for model, prices := range src {
		clone[model] = make(map[string]float64, len(prices))
		for resolution, price := range prices {
			clone[model][resolution] = price
		}
	}
	return clone
}

func cloneUsageModelRouting(src map[string][]int64) map[string][]int64 {
	if src == nil {
		return nil
	}
	clone := make(map[string][]int64, len(src))
	for model, accountIDs := range src {
		clone[model] = append([]int64(nil), accountIDs...)
	}
	return clone
}

func cloneUsageChannelModelPricing(src ChannelModelPricing) ChannelModelPricing {
	clone := src.Clone()
	clone.InputPrice = cloneUsagePointer(src.InputPrice)
	clone.OutputPrice = cloneUsagePointer(src.OutputPrice)
	clone.CacheWritePrice = cloneUsagePointer(src.CacheWritePrice)
	clone.CacheReadPrice = cloneUsagePointer(src.CacheReadPrice)
	clone.FastMultiplier = cloneUsagePointer(src.FastMultiplier)
	clone.FlexMultiplier = cloneUsagePointer(src.FlexMultiplier)
	clone.ImageInputPrice = cloneUsagePointer(src.ImageInputPrice)
	clone.ImageOutputPrice = cloneUsagePointer(src.ImageOutputPrice)
	clone.PerRequestPrice = cloneUsagePointer(src.PerRequestPrice)
	if src.Intervals != nil {
		clone.Intervals = make([]PricingInterval, len(src.Intervals))
		for i, interval := range src.Intervals {
			clone.Intervals[i] = interval
			clone.Intervals[i].MaxTokens = cloneUsagePointer(interval.MaxTokens)
			clone.Intervals[i].InputPrice = cloneUsagePointer(interval.InputPrice)
			clone.Intervals[i].OutputPrice = cloneUsagePointer(interval.OutputPrice)
			clone.Intervals[i].CacheWritePrice = cloneUsagePointer(interval.CacheWritePrice)
			clone.Intervals[i].CacheReadPrice = cloneUsagePointer(interval.CacheReadPrice)
			clone.Intervals[i].InputMultiplier = cloneUsagePointer(interval.InputMultiplier)
			clone.Intervals[i].OutputMultiplier = cloneUsagePointer(interval.OutputMultiplier)
			clone.Intervals[i].CacheWriteMultiplier = cloneUsagePointer(interval.CacheWriteMultiplier)
			clone.Intervals[i].CacheReadMultiplier = cloneUsagePointer(interval.CacheReadMultiplier)
			clone.Intervals[i].PerRequestPrice = cloneUsagePointer(interval.PerRequestPrice)
		}
	}
	return clone
}
