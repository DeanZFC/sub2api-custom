//go:build unit

package service

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestPrismRecordUsage_UnavailableUsageNeverChargesConfiguredPrices(t *testing.T) {
	const model = "gpt-5.1"
	const groupID, accountID = int64(182), int64(382)
	for _, pricing := range []struct {
		name              string
		channelPerRequest bool
		customStats       bool
	}{
		{name: "per_request_channel", channelPerRequest: true},
		{name: "custom_account_stats", customStats: true},
		{name: "both", channelPerRequest: true, customStats: true},
	} {
		for _, atomicBilling := range []bool{true, false} {
			for _, unavailable := range []bool{true, false} {
				name := fmt.Sprintf("%s/atomic=%t/unavailable=%t", pricing.name, atomicBilling, unavailable)
				t.Run(name, func(t *testing.T) {
					usageRepo := &openAIRecordUsageLogRepoStub{inserted: true}
					userRepo := &openAIRecordUsageUserRepoStub{}
					subRepo := &openAIRecordUsageSubRepoStub{}
					quota := &openAIRecordUsageAPIKeyQuotaStub{}
					billingRepo := &openAIRecordUsageBillingRepoStub{}
					svc := newOpenAIRecordUsageServiceForTest(usageRepo, userRepo, subRepo, nil)
					if atomicBilling {
						svc.usageBillingRepo = billingRepo
					}

					requestPrice, statsPrice := 0.25, 0.4
					inputPrice, outputPrice := 0.000001, 0.000002
					modelPricing := &ChannelModelPricing{
						Platform: PlatformOpenAI, Models: []string{model}, BillingMode: BillingModeToken,
						InputPrice: &inputPrice, OutputPrice: &outputPrice,
					}
					if pricing.channelPerRequest {
						modelPricing.BillingMode = BillingModePerRequest
						modelPricing.PerRequestPrice = &requestPrice
					}
					channel := &Channel{ID: 82, Status: StatusActive, ApplyPricingToAccountStats: true}
					if pricing.customStats {
						channel.AccountStatsPricingRules = []AccountStatsPricingRule{{
							ID: 1, AccountIDs: []int64{accountID},
							Pricing: []ChannelModelPricing{{
								Platform: PlatformOpenAI, Models: []string{model},
								BillingMode: BillingModePerRequest, PerRequestPrice: &statsPrice,
							}},
						}}
					}
					cache := newEmptyChannelCache()
					cache.pricingByGroupModel[channelModelKey{groupID: groupID, platform: PlatformOpenAI, model: model}] = modelPricing
					cache.channelByGroupID[groupID] = channel
					cache.groupPlatform[groupID] = PlatformOpenAI
					cache.loadedAt = time.Now()
					svc.channelService = &ChannelService{}
					svc.channelService.cache.Store(cache)
					svc.resolver = NewModelPricingResolver(svc.channelService, svc.billingService)

					group := &Group{ID: groupID, Platform: PlatformOpenAI, RateMultiplier: 1.2}
					user := &User{ID: 282}
					result := &OpenAIForwardResult{
						RequestID: "prism_usage_" + name, Model: model, UpstreamModel: model,
						UpstreamEndpoint: prismUpstreamEndpoint, UsageUnavailable: unavailable,
						Duration: 2 * time.Second, Stream: true,
					}
					if !unavailable {
						// Positive controls prove the configured prices are active and
						// reported Prism usage still follows ordinary billing.
						result.Usage = OpenAIUsage{InputTokens: 2, OutputTokens: 3}
					}
					err := svc.RecordUsage(context.Background(), &OpenAIRecordUsageInput{
						Result: result,
						APIKey: &APIKey{ID: 482, User: user, GroupID: i64p(groupID), Group: group, Quota: 100},
						User:   user,
						Account: &Account{
							ID: accountID, Platform: PlatformOpenAI, Type: AccountTypeOAuth,
							Extra: map[string]any{"prism": map[string]any{"enabled": true, "version": 1}},
						},
						InboundEndpoint: "/v1/responses", UpstreamEndpoint: prismUpstreamEndpoint,
						APIKeyService: quota,
					})
					require.NoError(t, err)
					require.Equal(t, 1, usageRepo.calls, "every completed request remains visible in the usage log")
					log := usageRepo.lastLog
					require.NotNil(t, log)
					require.Equal(t, result.RequestID, log.RequestID)
					require.Equal(t, accountID, log.AccountID)
					require.Equal(t, model, log.Model)
					require.Equal(t, prismUpstreamEndpoint, *log.UpstreamEndpoint)
					require.True(t, log.Stream)
					require.Equal(t, result.Usage.InputTokens+result.Usage.OutputTokens, log.TotalTokens())

					if unavailable {
						require.Zero(t, log.TotalCost)
						require.Zero(t, log.ActualCost)
						require.Nil(t, log.AccountStatsCost, "unknown usage must not acquire a custom per-request account cost")
						require.Equal(t, string(BillingModeToken), *log.BillingMode)
						require.Zero(t, userRepo.deductCalls)
						require.Zero(t, subRepo.incrementCalls)
						require.Zero(t, quota.quotaCalls)
						require.Zero(t, quota.rateLimitCalls)
					} else {
						expectedCost := 2*inputPrice + 3*outputPrice
						if pricing.channelPerRequest {
							expectedCost = requestPrice
						}
						require.InDelta(t, expectedCost, log.TotalCost, 1e-12)
						require.InDelta(t, expectedCost*group.RateMultiplier, log.ActualCost, 1e-12)
						require.NotNil(t, log.AccountStatsCost)
						if pricing.customStats {
							require.InDelta(t, statsPrice, *log.AccountStatsCost, 1e-12)
						} else {
							require.InDelta(t, expectedCost, *log.AccountStatsCost, 1e-12)
						}
						if !atomicBilling {
							require.Equal(t, 1, userRepo.deductCalls)
							require.InDelta(t, log.ActualCost, userRepo.lastAmount, 1e-12)
						}
					}
					if atomicBilling {
						require.Equal(t, 1, billingRepo.calls)
						cmd := billingRepo.lastCmd
						require.NotNil(t, cmd)
						require.InDelta(t, log.ActualCost, cmd.BalanceCost, 1e-12)
						require.InDelta(t, log.ActualCost, cmd.APIKeyQuotaCost, 1e-12)
						require.Zero(t, cmd.SubscriptionCost)
						require.Zero(t, cmd.AccountQuotaCost)
						if unavailable {
							require.Zero(t, cmd.APIKeyRateLimitCost)
						}
					}
				})
			}
		}
	}
}
