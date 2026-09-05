package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/dgraph-io/ristretto"
)

const apiKeyAuthSnapshotVersion = 24 // v24: fallback billing plus official OpenAI fast fields

type apiKeyAuthCacheConfig struct {
	l1Size        int
	l1TTL         time.Duration
	l2TTL         time.Duration
	negativeTTL   time.Duration
	jitterPercent int
	singleflight  bool
}

func newAPIKeyAuthCacheConfig(cfg *config.Config) apiKeyAuthCacheConfig {
	if cfg == nil {
		return apiKeyAuthCacheConfig{}
	}
	auth := cfg.APIKeyAuth
	return apiKeyAuthCacheConfig{
		l1Size:        auth.L1Size,
		l1TTL:         time.Duration(auth.L1TTLSeconds) * time.Second,
		l2TTL:         time.Duration(auth.L2TTLSeconds) * time.Second,
		negativeTTL:   time.Duration(auth.NegativeTTLSeconds) * time.Second,
		jitterPercent: auth.JitterPercent,
		singleflight:  auth.Singleflight,
	}
}

func (c apiKeyAuthCacheConfig) l1Enabled() bool {
	return c.l1Size > 0 && c.l1TTL > 0
}

func (c apiKeyAuthCacheConfig) l2Enabled() bool {
	return c.l2TTL > 0
}

func (c apiKeyAuthCacheConfig) negativeEnabled() bool {
	return c.negativeTTL > 0
}

// jitterTTL 为缓存 TTL 添加抖动，避免多个请求在同一时刻同时过期触发集中回源。
// 这里直接使用 rand/v2 的顶层函数：并发安全，无需全局互斥锁。
func (c apiKeyAuthCacheConfig) jitterTTL(ttl time.Duration) time.Duration {
	if ttl <= 0 {
		return ttl
	}
	if c.jitterPercent <= 0 {
		return ttl
	}
	percent := c.jitterPercent
	if percent > 100 {
		percent = 100
	}
	delta := float64(percent) / 100
	randVal := rand.Float64()
	factor := 1 - delta + randVal*(2*delta)
	if factor <= 0 {
		return ttl
	}
	return time.Duration(float64(ttl) * factor)
}

func (s *APIKeyService) initAuthCache(cfg *config.Config) {
	s.authCfg = newAPIKeyAuthCacheConfig(cfg)
	if s.authCfg.negativeEnabled() {
		negativeSize := defaultNegativeAuthCacheSize
		if s.authCfg.l1Size > 0 && s.authCfg.l1Size < negativeSize {
			negativeSize = s.authCfg.l1Size
		}
		cache, err := ristretto.NewCache(&ristretto.Config{
			NumCounters: int64(negativeSize) * 10,
			MaxCost:     int64(negativeSize),
			BufferItems: 64,
		})
		if err == nil {
			s.authNegativeCacheL1 = cache
		}
	}
	if s.authCfg.l1Enabled() {
		cache, err := ristretto.NewCache(&ristretto.Config{
			NumCounters: int64(s.authCfg.l1Size) * 10,
			MaxCost:     int64(s.authCfg.l1Size),
			BufferItems: 64,
		})
		if err == nil {
			s.authCacheL1 = cache
		}
	}
}

// StartAuthCacheInvalidationSubscriber starts the Pub/Sub subscriber for L1 cache invalidation.
// This should be called after the service is fully initialized.
func (s *APIKeyService) StartAuthCacheInvalidationSubscriber(ctx context.Context) {
	if s.cache == nil || (s.authCacheL1 == nil && s.authNegativeCacheL1 == nil) {
		return
	}
	s.authInvalidationStart.Do(func() {
		subscriberCtx, cancel := context.WithCancel(ctx)
		subscriberCtx = withAuthCacheSubscriptionReady(subscriberCtx, func() {
			s.authInvalidationConnected.Store(true)
		})
		s.authInvalidationCancel = cancel
		s.authInvalidationWG.Add(1)
		go func() {
			defer s.authInvalidationWG.Done()
			backoff := time.Second
			for {
				err := s.cache.SubscribeAuthCacheInvalidation(subscriberCtx, func(cacheKey string) {
					s.invalidateLocalAuthCache(cacheKey)
				})
				wasConnected := s.authInvalidationConnected.Swap(false)
				if subscriberCtx.Err() != nil {
					return
				}
				if wasConnected {
					backoff = time.Second
				}
				s.authInvalidationFailures.Add(1)
				if err == nil {
					err = errors.New("auth cache invalidation subscription closed")
				}
				slog.Warn("failed to start auth cache invalidation subscriber; retrying", "error", err, "retry_in", backoff)
				timer := time.NewTimer(backoff)
				select {
				case <-subscriberCtx.Done():
					timer.Stop()
					return
				case <-timer.C:
				}
				if backoff < 30*time.Second {
					backoff *= 2
					if backoff > 30*time.Second {
						backoff = 30 * time.Second
					}
				}
			}
		}()
	})
}

func (s *APIKeyService) invalidateLocalAuthCache(cacheKey string) {
	if s == nil {
		return
	}
	if s.authCacheL1 != nil {
		s.authCacheL1.Del(cacheKey)
	}
	if s.authNegativeCacheL1 != nil {
		s.authNegativeCacheL1.Del(cacheKey)
	}
}

type AuthCacheInvalidationSubscriberHealth struct {
	Connected bool   `json:"connected"`
	Failures  uint64 `json:"failures"`
}

func (s *APIKeyService) AuthCacheInvalidationSubscriberHealth() AuthCacheInvalidationSubscriberHealth {
	if s == nil {
		return AuthCacheInvalidationSubscriberHealth{}
	}
	return AuthCacheInvalidationSubscriberHealth{
		Connected: s.authInvalidationConnected.Load(),
		Failures:  s.authInvalidationFailures.Load(),
	}
}

func (s *APIKeyService) StopAuthCacheInvalidationSubscriber() {
	if s == nil {
		return
	}
	s.authInvalidationStop.Do(func() {
		if s.authInvalidationCancel != nil {
			s.authInvalidationCancel()
		}
		s.authInvalidationWG.Wait()
	})
}

func (s *APIKeyService) authCacheKey(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:])
}

func (s *APIKeyService) getAuthCacheEntry(ctx context.Context, cacheKey string) (*APIKeyAuthCacheEntry, bool) {
	if s.authCacheL1 != nil {
		if val, ok := s.authCacheL1.Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok {
				return entry, true
			}
		}
	}
	if s.authNegativeCacheL1 != nil {
		if val, ok := s.authNegativeCacheL1.Get(cacheKey); ok {
			if entry, ok := val.(*APIKeyAuthCacheEntry); ok && entry.NotFound {
				return entry, true
			}
		}
	}
	if s.cache == nil || !s.authCfg.l2Enabled() {
		return nil, false
	}
	entry, err := s.cache.GetAuthCache(ctx, cacheKey)
	if err != nil {
		return nil, false
	}
	s.setAuthCacheL1(cacheKey, entry)
	return entry, true
}

func (s *APIKeyService) setAuthCacheL1(cacheKey string, entry *APIKeyAuthCacheEntry) {
	if entry == nil {
		return
	}
	if entry.NotFound {
		if s.authNegativeCacheL1 != nil && s.authCfg.negativeTTL > 0 {
			_ = s.authNegativeCacheL1.SetWithTTL(cacheKey, entry, 1, s.authCfg.jitterTTL(s.authCfg.negativeTTL))
		}
		return
	}
	if s.authCacheL1 == nil {
		return
	}
	ttl := s.authCfg.l1TTL
	ttl = s.authCfg.jitterTTL(ttl)
	_ = s.authCacheL1.SetWithTTL(cacheKey, entry, 1, ttl)
}

func (s *APIKeyService) setAuthCacheEntry(ctx context.Context, cacheKey string, entry *APIKeyAuthCacheEntry, ttl time.Duration) {
	if entry == nil {
		return
	}
	s.setAuthCacheL1(cacheKey, entry)
	if s.cache == nil || !s.authCfg.l2Enabled() {
		return
	}
	_ = s.cache.SetAuthCache(ctx, cacheKey, entry, s.authCfg.jitterTTL(ttl))
}

func (s *APIKeyService) deleteAuthCache(ctx context.Context, cacheKey string) {
	if s.authCacheL1 != nil {
		s.authCacheL1.Del(cacheKey)
	}
	if s.authNegativeCacheL1 != nil {
		s.authNegativeCacheL1.Del(cacheKey)
	}
	if s.cache == nil {
		return
	}
	_ = s.cache.DeleteAuthCache(ctx, cacheKey)
	// Publish invalidation message to other instances
	_ = s.cache.PublishAuthCacheInvalidation(ctx, cacheKey)
}

func (s *APIKeyService) loadAuthCacheEntry(ctx context.Context, key, cacheKey string) (*APIKeyAuthCacheEntry, error) {
	apiKey, err := s.lookupAPIKeyForAuth(ctx, key)
	if err != nil {
		if errors.Is(err, ErrAPIKeyNotFound) {
			entry := &APIKeyAuthCacheEntry{NotFound: true}
			if s.authCfg.negativeEnabled() {
				// Invalid keys are attacker-controlled and high-cardinality. Keep their
				// negative entries in the bounded process-local cache; do not amplify
				// random-key scans into Redis writes on every instance.
				s.setAuthCacheL1(cacheKey, entry)
			}
			return entry, nil
		}
		return nil, fmt.Errorf("get api key: %w", err)
	}
	apiKey.Key = key
	snapshot := s.snapshotFromAPIKey(ctx, apiKey)
	if snapshot == nil {
		return nil, fmt.Errorf("get api key: %w", ErrAPIKeyNotFound)
	}
	entry := &APIKeyAuthCacheEntry{Snapshot: snapshot}
	s.setAuthCacheEntry(ctx, cacheKey, entry, s.authCfg.l2TTL)
	return entry, nil
}

func (s *APIKeyService) lookupAPIKeyForAuth(ctx context.Context, key string) (*APIKey, error) {
	if s == nil || s.apiKeyRepo == nil {
		return nil, ErrAPIKeyNotFound
	}
	if s.authLookupSlots == nil {
		return s.apiKeyRepo.GetByKeyForAuth(ctx, key)
	}
	s.authLookupTotal.Add(1)
	select {
	case s.authLookupSlots <- struct{}{}:
		s.authLookupInFlight.Add(1)
		defer func() {
			s.authLookupInFlight.Add(-1)
			<-s.authLookupSlots
		}()
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
		s.authLookupRejected.Add(1)
		return nil, ErrAPIKeyAuthOverloaded
	}
	return s.apiKeyRepo.GetByKeyForAuth(ctx, key)
}

func (s *APIKeyService) applyAuthCacheEntry(key string, entry *APIKeyAuthCacheEntry) (*APIKey, bool, error) {
	if entry == nil {
		return nil, false, nil
	}
	if entry.NotFound {
		return nil, true, ErrAPIKeyNotFound
	}
	if entry.Snapshot == nil {
		return nil, false, nil
	}
	if entry.Snapshot.Version != apiKeyAuthSnapshotVersion {
		return nil, false, nil
	}
	return s.snapshotToAPIKey(key, entry.Snapshot), true, nil
}

func apiKeyAuthGroupSnapshotFromGroup(group *Group) *APIKeyAuthGroupSnapshot {
	if group == nil {
		return nil
	}
	return &APIKeyAuthGroupSnapshot{
		ID:                              group.ID,
		Name:                            group.Name,
		Description:                     group.Description,
		Platform:                        group.Platform,
		IsExclusive:                     group.IsExclusive,
		Status:                          group.Status,
		SubscriptionType:                group.SubscriptionType,
		RateMultiplier:                  group.RateMultiplier,
		DailyLimitUSD:                   group.DailyLimitUSD,
		WeeklyLimitUSD:                  group.WeeklyLimitUSD,
		MonthlyLimitUSD:                 group.MonthlyLimitUSD,
		DefaultValidityDays:             group.DefaultValidityDays,
		AllowImageGeneration:            group.AllowImageGeneration,
		AllowBatchImageGeneration:       group.AllowBatchImageGeneration,
		ImageRateIndependent:            group.ImageRateIndependent,
		ImageRateMultiplier:             group.ImageRateMultiplier,
		ImagePrice1K:                    group.ImagePrice1K,
		ImagePrice2K:                    group.ImagePrice2K,
		ImagePrice4K:                    group.ImagePrice4K,
		BatchImageDiscountMultiplier:    group.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        group.BatchImageHoldMultiplier,
		VideoRateIndependent:            group.VideoRateIndependent,
		VideoRateMultiplier:             group.VideoRateMultiplier,
		VideoPrice480P:                  group.VideoPrice480P,
		VideoPrice720P:                  group.VideoPrice720P,
		VideoPrice1080P:                 group.VideoPrice1080P,
		VideoModelPrices:                NormalizeVideoModelPrices(group.VideoModelPrices),
		WebSearchPricePerCall:           group.WebSearchPricePerCall,
		SearchPricePer1k:                group.SearchPricePer1k,
		AudioRealtimePricePerMin:        group.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    group.AudioTTSPricePerMillionChars,
		AudioSTTPricePerHour:            group.AudioSTTPricePerHour,
		LongContextPricingEnabled:       group.LongContextPricingEnabled,
		ModelPricing:                    group.ModelPricing,
		ClaudeCodeOnly:                  group.ClaudeCodeOnly,
		FallbackGroupID:                 group.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: group.FallbackGroupIDOnInvalidRequest,
		ModelRouting:                    group.ModelRouting,
		ModelRoutingEnabled:             group.ModelRoutingEnabled,
		MCPXMLInject:                    group.MCPXMLInject,
		SupportedModelScopes:            group.SupportedModelScopes,
		SortOrder:                       group.SortOrder,
		AllowMessagesDispatch:           group.AllowMessagesDispatch,
		AllowLive:                       group.AllowLive,
		ForceOpenAIFast:                 group.ForceOpenAIFast,
		FreeOpenAIFast:                  group.FreeOpenAIFast,
		RequireOAuthOnly:                group.RequireOAuthOnly,
		RequirePrivacySet:               group.RequirePrivacySet,
		DefaultMappedModel:              group.DefaultMappedModel,
		MessagesDispatchModelConfig:     group.MessagesDispatchModelConfig,
		ModelsListConfig:                group.ModelsListConfig,
		RPMLimit:                        group.RPMLimit,
		UserConcurrencyLimit:            group.UserConcurrencyLimit,
		MaxReasoningEffort:              group.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     group.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         group.ReasoningEffortMappings,
		PeakRateEnabled:                 group.PeakRateEnabled,
		PeakStart:                       group.PeakStart,
		PeakEnd:                         group.PeakEnd,
		PeakRateMultiplier:              group.PeakRateMultiplier,
		ProfitControlEnabled:            group.ProfitControlEnabled,
		ProfitMinMargin:                 group.ProfitMinMargin,
		ProfitSafetyBuffer:              group.ProfitSafetyBuffer,
	}
}

func apiKeyAuthGroupFromSnapshot(snapshot *APIKeyAuthGroupSnapshot) *Group {
	if snapshot == nil {
		return nil
	}
	return &Group{
		ID:                              snapshot.ID,
		Name:                            snapshot.Name,
		Description:                     snapshot.Description,
		Platform:                        snapshot.Platform,
		IsExclusive:                     snapshot.IsExclusive,
		Status:                          snapshot.Status,
		Hydrated:                        true,
		SubscriptionType:                snapshot.SubscriptionType,
		RateMultiplier:                  snapshot.RateMultiplier,
		DailyLimitUSD:                   snapshot.DailyLimitUSD,
		WeeklyLimitUSD:                  snapshot.WeeklyLimitUSD,
		MonthlyLimitUSD:                 snapshot.MonthlyLimitUSD,
		DefaultValidityDays:             snapshot.DefaultValidityDays,
		AllowImageGeneration:            snapshot.AllowImageGeneration,
		AllowBatchImageGeneration:       snapshot.AllowBatchImageGeneration,
		ImageRateIndependent:            snapshot.ImageRateIndependent,
		ImageRateMultiplier:             snapshot.ImageRateMultiplier,
		ImagePrice1K:                    snapshot.ImagePrice1K,
		ImagePrice2K:                    snapshot.ImagePrice2K,
		ImagePrice4K:                    snapshot.ImagePrice4K,
		BatchImageDiscountMultiplier:    snapshot.BatchImageDiscountMultiplier,
		BatchImageHoldMultiplier:        snapshot.BatchImageHoldMultiplier,
		VideoRateIndependent:            snapshot.VideoRateIndependent,
		VideoRateMultiplier:             snapshot.VideoRateMultiplier,
		VideoPrice480P:                  snapshot.VideoPrice480P,
		VideoPrice720P:                  snapshot.VideoPrice720P,
		VideoPrice1080P:                 snapshot.VideoPrice1080P,
		VideoModelPrices:                NormalizeVideoModelPrices(snapshot.VideoModelPrices),
		WebSearchPricePerCall:           snapshot.WebSearchPricePerCall,
		SearchPricePer1k:                snapshot.SearchPricePer1k,
		AudioRealtimePricePerMin:        snapshot.AudioRealtimePricePerMin,
		AudioTTSPricePerMillionChars:    snapshot.AudioTTSPricePerMillionChars,
		AudioSTTPricePerHour:            snapshot.AudioSTTPricePerHour,
		LongContextPricingEnabled:       snapshot.LongContextPricingEnabled,
		ModelPricing:                    snapshot.ModelPricing,
		ClaudeCodeOnly:                  snapshot.ClaudeCodeOnly,
		FallbackGroupID:                 snapshot.FallbackGroupID,
		FallbackGroupIDOnInvalidRequest: snapshot.FallbackGroupIDOnInvalidRequest,
		ModelRouting:                    snapshot.ModelRouting,
		ModelRoutingEnabled:             snapshot.ModelRoutingEnabled,
		MCPXMLInject:                    snapshot.MCPXMLInject,
		SupportedModelScopes:            snapshot.SupportedModelScopes,
		SortOrder:                       snapshot.SortOrder,
		AllowMessagesDispatch:           snapshot.AllowMessagesDispatch,
		AllowLive:                       snapshot.AllowLive,
		ForceOpenAIFast:                 snapshot.ForceOpenAIFast,
		FreeOpenAIFast:                  snapshot.FreeOpenAIFast,
		RequireOAuthOnly:                snapshot.RequireOAuthOnly,
		RequirePrivacySet:               snapshot.RequirePrivacySet,
		DefaultMappedModel:              snapshot.DefaultMappedModel,
		MessagesDispatchModelConfig:     snapshot.MessagesDispatchModelConfig,
		ModelsListConfig:                snapshot.ModelsListConfig,
		RPMLimit:                        snapshot.RPMLimit,
		UserConcurrencyLimit:            snapshot.UserConcurrencyLimit,
		MaxReasoningEffort:              snapshot.MaxReasoningEffort,
		MaxReasoningEffortOverLimit:     snapshot.MaxReasoningEffortOverLimit,
		ReasoningEffortMappings:         snapshot.ReasoningEffortMappings,
		PeakRateEnabled:                 snapshot.PeakRateEnabled,
		PeakStart:                       snapshot.PeakStart,
		PeakEnd:                         snapshot.PeakEnd,
		PeakRateMultiplier:              snapshot.PeakRateMultiplier,
		ProfitControlEnabled:            snapshot.ProfitControlEnabled,
		ProfitMinMargin:                 snapshot.ProfitMinMargin,
		ProfitSafetyBuffer:              snapshot.ProfitSafetyBuffer,
	}
}

func (s *APIKeyService) snapshotFromAPIKey(ctx context.Context, apiKey *APIKey) *APIKeyAuthSnapshot {
	if apiKey == nil || apiKey.User == nil {
		return nil
	}
	snapshot := &APIKeyAuthSnapshot{
		Version:         apiKeyAuthSnapshotVersion,
		APIKeyID:        apiKey.ID,
		UserID:          apiKey.UserID,
		GroupID:         cloneInt64Ptr(apiKey.GroupID),
		FallbackGroupID: cloneInt64Ptr(apiKey.FallbackGroupID),
		Name:            apiKey.Name,
		Status:          apiKey.Status,
		IPWhitelist:     apiKey.IPWhitelist,
		IPBlacklist:     apiKey.IPBlacklist,
		Quota:           apiKey.Quota,
		QuotaUsed:       apiKey.QuotaUsed,
		ExpiresAt:       apiKey.ExpiresAt,
		RateLimit5h:     apiKey.RateLimit5h,
		RateLimit1d:     apiKey.RateLimit1d,
		RateLimit7d:     apiKey.RateLimit7d,
		User: APIKeyAuthUserSnapshot{
			ID:                         apiKey.User.ID,
			Status:                     apiKey.User.Status,
			Role:                       apiKey.User.Role,
			Balance:                    apiKey.User.Balance,
			Concurrency:                apiKey.User.Concurrency,
			AllowedGroups:              apiKey.User.AllowedGroups,
			Email:                      apiKey.User.Email,
			Username:                   apiKey.User.Username,
			BalanceNotifyEnabled:       apiKey.User.BalanceNotifyEnabled,
			RestrictPublicGroups:       apiKey.User.RestrictPublicGroups,
			BalanceNotifyThresholdType: apiKey.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     apiKey.User.BalanceNotifyThreshold,
			BalanceNotifyExtraEmails:   apiKey.User.BalanceNotifyExtraEmails,
			TotalRecharged:             apiKey.User.TotalRecharged,
			RPMLimit:                   apiKey.User.RPMLimit,
		},
	}

	// 填充 (user, group) RPM override —— snapshot 构建时查一次 DB，后续请求零 DB 往返。
	if apiKey.GroupID != nil && *apiKey.GroupID > 0 && s.userGroupRateRepo != nil {
		override, err := s.userGroupRateRepo.GetRPMOverrideByUserAndGroup(ctx, apiKey.UserID, *apiKey.GroupID)
		if err == nil && override != nil {
			snapshot.User.UserGroupRPMOverride = cloneIntPtr(override)
		}
		// 查询失败或无 override 时留 nil，checkRPM 会回退到 DB 查询
	}
	if apiKey.Group != nil {
		snapshot.Group = &APIKeyAuthGroupSnapshot{
			ID:                              apiKey.Group.ID,
			Name:                            apiKey.Group.Name,
			Description:                     apiKey.Group.Description,
			Platform:                        apiKey.Group.Platform,
			IsExclusive:                     apiKey.Group.IsExclusive,
			Status:                          apiKey.Group.Status,
			SubscriptionType:                apiKey.Group.SubscriptionType,
			RateMultiplier:                  apiKey.Group.RateMultiplier,
			DailyLimitUSD:                   apiKey.Group.DailyLimitUSD,
			WeeklyLimitUSD:                  apiKey.Group.WeeklyLimitUSD,
			MonthlyLimitUSD:                 apiKey.Group.MonthlyLimitUSD,
			DefaultValidityDays:             apiKey.Group.DefaultValidityDays,
			AllowImageGeneration:            apiKey.Group.AllowImageGeneration,
			AllowBatchImageGeneration:       apiKey.Group.AllowBatchImageGeneration,
			ImageRateIndependent:            apiKey.Group.ImageRateIndependent,
			ImageRateMultiplier:             apiKey.Group.ImageRateMultiplier,
			ImagePrice1K:                    apiKey.Group.ImagePrice1K,
			ImagePrice2K:                    apiKey.Group.ImagePrice2K,
			ImagePrice4K:                    apiKey.Group.ImagePrice4K,
			BatchImageDiscountMultiplier:    apiKey.Group.BatchImageDiscountMultiplier,
			BatchImageHoldMultiplier:        apiKey.Group.BatchImageHoldMultiplier,
			VideoRateIndependent:            apiKey.Group.VideoRateIndependent,
			VideoRateMultiplier:             apiKey.Group.VideoRateMultiplier,
			VideoPrice480P:                  apiKey.Group.VideoPrice480P,
			VideoPrice720P:                  apiKey.Group.VideoPrice720P,
			VideoPrice1080P:                 apiKey.Group.VideoPrice1080P,
			VideoModelPrices:                NormalizeVideoModelPrices(apiKey.Group.VideoModelPrices),
			WebSearchPricePerCall:           apiKey.Group.WebSearchPricePerCall,
			SearchPricePer1k:                apiKey.Group.SearchPricePer1k,
			AudioRealtimePricePerMin:        apiKey.Group.AudioRealtimePricePerMin,
			AudioTTSPricePerMillionChars:    apiKey.Group.AudioTTSPricePerMillionChars,
			AudioSTTPricePerHour:            apiKey.Group.AudioSTTPricePerHour,
			LongContextPricingEnabled:       apiKey.Group.LongContextPricingEnabled,
			ModelPricing:                    apiKey.Group.ModelPricing,
			ClaudeCodeOnly:                  apiKey.Group.ClaudeCodeOnly,
			FallbackGroupID:                 apiKey.Group.FallbackGroupID,
			FallbackGroupIDOnInvalidRequest: apiKey.Group.FallbackGroupIDOnInvalidRequest,
			ModelRouting:                    apiKey.Group.ModelRouting,
			ModelRoutingEnabled:             apiKey.Group.ModelRoutingEnabled,
			MCPXMLInject:                    apiKey.Group.MCPXMLInject,
			SupportedModelScopes:            apiKey.Group.SupportedModelScopes,
			SortOrder:                       apiKey.Group.SortOrder,
			AllowMessagesDispatch:           apiKey.Group.AllowMessagesDispatch,
			AllowLive:                       apiKey.Group.AllowLive,
			RequireOAuthOnly:                apiKey.Group.RequireOAuthOnly,
			RequirePrivacySet:               apiKey.Group.RequirePrivacySet,
			ForceOpenAIFast:                 apiKey.Group.ForceOpenAIFast,
			FreeOpenAIFast:                  apiKey.Group.FreeOpenAIFast,
			DefaultMappedModel:              apiKey.Group.DefaultMappedModel,
			MessagesDispatchModelConfig:     apiKey.Group.MessagesDispatchModelConfig,
			ModelsListConfig:                apiKey.Group.ModelsListConfig,
			CodexModelsManifestConfig:       apiKey.Group.CodexModelsManifestConfig,
			RPMLimit:                        apiKey.Group.RPMLimit,
			UserConcurrencyLimit:            apiKey.Group.UserConcurrencyLimit,
			MaxReasoningEffort:              apiKey.Group.MaxReasoningEffort,
			MaxReasoningEffortOverLimit:     apiKey.Group.MaxReasoningEffortOverLimit,
			ReasoningEffortMappings:         apiKey.Group.ReasoningEffortMappings,
			PeakRateEnabled:                 apiKey.Group.PeakRateEnabled,
			PeakStart:                       apiKey.Group.PeakStart,
			PeakEnd:                         apiKey.Group.PeakEnd,
			PeakRateMultiplier:              apiKey.Group.PeakRateMultiplier,
			ProfitControlEnabled:            apiKey.Group.ProfitControlEnabled,
			ProfitMinMargin:                 apiKey.Group.ProfitMinMargin,
			ProfitSafetyBuffer:              apiKey.Group.ProfitSafetyBuffer,
		}
	}
	if apiKey.FallbackGroup != nil && apiKey.FallbackGroup.IsActive() && s.canUserBindGroup(ctx, apiKey.User, apiKey.FallbackGroup) {
		snapshot.FallbackGroup = apiKeyAuthGroupSnapshotFromGroup(apiKey.FallbackGroup)
	}
	return snapshot
}

func (s *APIKeyService) snapshotToAPIKey(key string, snapshot *APIKeyAuthSnapshot) *APIKey {
	if snapshot == nil {
		return nil
	}
	apiKey := &APIKey{
		ID:     snapshot.APIKeyID,
		UserID: snapshot.UserID,
		// Every request gets private ID pointers. Fallback activation mutates the
		// effective GroupID in place so parsed request objects follow the route;
		// sharing the snapshot pointer would otherwise corrupt the auth cache and
		// leak one request's fallback state into subsequent requests.
		GroupID:         cloneInt64Ptr(snapshot.GroupID),
		FallbackGroupID: cloneInt64Ptr(snapshot.FallbackGroupID),
		Key:             key,
		Name:            snapshot.Name,
		Status:          snapshot.Status,
		IPWhitelist:     snapshot.IPWhitelist,
		IPBlacklist:     snapshot.IPBlacklist,
		Quota:           snapshot.Quota,
		QuotaUsed:       snapshot.QuotaUsed,
		ExpiresAt:       snapshot.ExpiresAt,
		RateLimit5h:     snapshot.RateLimit5h,
		RateLimit1d:     snapshot.RateLimit1d,
		RateLimit7d:     snapshot.RateLimit7d,
		User: &User{
			ID:                         snapshot.User.ID,
			Status:                     snapshot.User.Status,
			Role:                       snapshot.User.Role,
			Balance:                    snapshot.User.Balance,
			Concurrency:                snapshot.User.Concurrency,
			AllowedGroups:              snapshot.User.AllowedGroups,
			Email:                      snapshot.User.Email,
			Username:                   snapshot.User.Username,
			BalanceNotifyEnabled:       snapshot.User.BalanceNotifyEnabled,
			RestrictPublicGroups:       snapshot.User.RestrictPublicGroups,
			BalanceNotifyThresholdType: snapshot.User.BalanceNotifyThresholdType,
			BalanceNotifyThreshold:     snapshot.User.BalanceNotifyThreshold,
			BalanceNotifyExtraEmails:   snapshot.User.BalanceNotifyExtraEmails,
			TotalRecharged:             snapshot.User.TotalRecharged,
			RPMLimit:                   snapshot.User.RPMLimit,
			UserGroupRPMOverride:       cloneIntPtr(snapshot.User.UserGroupRPMOverride),
		},
	}
	if snapshot.Group != nil {
		apiKey.Group = &Group{
			ID:                              snapshot.Group.ID,
			Name:                            snapshot.Group.Name,
			Description:                     snapshot.Group.Description,
			Platform:                        snapshot.Group.Platform,
			IsExclusive:                     snapshot.Group.IsExclusive,
			Status:                          snapshot.Group.Status,
			Hydrated:                        true,
			SubscriptionType:                snapshot.Group.SubscriptionType,
			RateMultiplier:                  snapshot.Group.RateMultiplier,
			DailyLimitUSD:                   snapshot.Group.DailyLimitUSD,
			WeeklyLimitUSD:                  snapshot.Group.WeeklyLimitUSD,
			MonthlyLimitUSD:                 snapshot.Group.MonthlyLimitUSD,
			DefaultValidityDays:             snapshot.Group.DefaultValidityDays,
			AllowImageGeneration:            snapshot.Group.AllowImageGeneration,
			AllowBatchImageGeneration:       snapshot.Group.AllowBatchImageGeneration,
			ImageRateIndependent:            snapshot.Group.ImageRateIndependent,
			ImageRateMultiplier:             snapshot.Group.ImageRateMultiplier,
			ImagePrice1K:                    snapshot.Group.ImagePrice1K,
			ImagePrice2K:                    snapshot.Group.ImagePrice2K,
			ImagePrice4K:                    snapshot.Group.ImagePrice4K,
			BatchImageDiscountMultiplier:    snapshot.Group.BatchImageDiscountMultiplier,
			BatchImageHoldMultiplier:        snapshot.Group.BatchImageHoldMultiplier,
			VideoRateIndependent:            snapshot.Group.VideoRateIndependent,
			VideoRateMultiplier:             snapshot.Group.VideoRateMultiplier,
			VideoPrice480P:                  snapshot.Group.VideoPrice480P,
			VideoPrice720P:                  snapshot.Group.VideoPrice720P,
			VideoPrice1080P:                 snapshot.Group.VideoPrice1080P,
			VideoModelPrices:                NormalizeVideoModelPrices(snapshot.Group.VideoModelPrices),
			WebSearchPricePerCall:           snapshot.Group.WebSearchPricePerCall,
			SearchPricePer1k:                snapshot.Group.SearchPricePer1k,
			AudioRealtimePricePerMin:        snapshot.Group.AudioRealtimePricePerMin,
			AudioTTSPricePerMillionChars:    snapshot.Group.AudioTTSPricePerMillionChars,
			AudioSTTPricePerHour:            snapshot.Group.AudioSTTPricePerHour,
			LongContextPricingEnabled:       snapshot.Group.LongContextPricingEnabled,
			ModelPricing:                    snapshot.Group.ModelPricing,
			ClaudeCodeOnly:                  snapshot.Group.ClaudeCodeOnly,
			FallbackGroupID:                 snapshot.Group.FallbackGroupID,
			FallbackGroupIDOnInvalidRequest: snapshot.Group.FallbackGroupIDOnInvalidRequest,
			ModelRouting:                    snapshot.Group.ModelRouting,
			ModelRoutingEnabled:             snapshot.Group.ModelRoutingEnabled,
			MCPXMLInject:                    snapshot.Group.MCPXMLInject,
			SupportedModelScopes:            snapshot.Group.SupportedModelScopes,
			SortOrder:                       snapshot.Group.SortOrder,
			AllowMessagesDispatch:           snapshot.Group.AllowMessagesDispatch,
			AllowLive:                       snapshot.Group.AllowLive,
			RequireOAuthOnly:                snapshot.Group.RequireOAuthOnly,
			RequirePrivacySet:               snapshot.Group.RequirePrivacySet,
			ForceOpenAIFast:                 snapshot.Group.ForceOpenAIFast,
			FreeOpenAIFast:                  snapshot.Group.FreeOpenAIFast,
			DefaultMappedModel:              snapshot.Group.DefaultMappedModel,
			MessagesDispatchModelConfig:     snapshot.Group.MessagesDispatchModelConfig,
			ModelsListConfig:                snapshot.Group.ModelsListConfig,
			CodexModelsManifestConfig:       snapshot.Group.CodexModelsManifestConfig,
			RPMLimit:                        snapshot.Group.RPMLimit,
			UserConcurrencyLimit:            snapshot.Group.UserConcurrencyLimit,
			MaxReasoningEffort:              snapshot.Group.MaxReasoningEffort,
			MaxReasoningEffortOverLimit:     snapshot.Group.MaxReasoningEffortOverLimit,
			ReasoningEffortMappings:         snapshot.Group.ReasoningEffortMappings,
			PeakRateEnabled:                 snapshot.Group.PeakRateEnabled,
			PeakStart:                       snapshot.Group.PeakStart,
			PeakEnd:                         snapshot.Group.PeakEnd,
			PeakRateMultiplier:              snapshot.Group.PeakRateMultiplier,
			ProfitControlEnabled:            snapshot.Group.ProfitControlEnabled,
			ProfitMinMargin:                 snapshot.Group.ProfitMinMargin,
			ProfitSafetyBuffer:              snapshot.Group.ProfitSafetyBuffer,
		}
	}
	if snapshot.FallbackGroup != nil {
		apiKey.FallbackGroup = apiKeyAuthGroupFromSnapshot(snapshot.FallbackGroup)
	}
	s.compileAPIKeyIPRules(apiKey)
	return apiKey
}

func cloneIntPtr(value *int) *int {
	if value == nil {
		return nil
	}
	copy := *value
	return &copy
}
