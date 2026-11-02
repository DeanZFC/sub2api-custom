//go:build unit

package handler

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type fallbackReservationCache struct{ *handlerInflightCache }

func (c *fallbackReservationCache) GetSubscriptionCache(context.Context, int64, int64) (*service.SubscriptionCacheData, error) {
	return &service.SubscriptionCacheData{Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type fallbackReservationSubRepo struct {
	service.UserSubscriptionRepository
}

func (r fallbackReservationSubRepo) GetActiveByUserIDAndGroupID(_ context.Context, user, group int64) (*service.UserSubscription, error) {
	return &service.UserSubscription{ID: group + 100, UserID: user, GroupID: group, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}, nil
}

type fallbackPriceEstimator struct{}

func (fallbackPriceEstimator) EstimateInflightReservation(_ context.Context, key *service.APIKey, _ service.InflightEstimateRequest) (float64, bool) {
	return key.Group.RateMultiplier, true
}

func TestAPIKeyFallbackRepricesReservationAndRetainsItForUsage(t *testing.T) {
	for _, modes := range [][2]string{
		{service.SubscriptionTypeStandard, service.SubscriptionTypeStandard},
		{service.SubscriptionTypeSubscription, service.SubscriptionTypeStandard},
		{service.SubscriptionTypeStandard, service.SubscriptionTypeSubscription},
	} {
		t.Run(modes[0]+"_to_"+modes[1], func(t *testing.T) {
			primary, target := int64(10), int64(20)
			key := &service.APIKey{ID: 1, User: &service.User{ID: 7}, GroupID: &primary, FallbackGroupID: &target,
				Group:         &service.Group{ID: 10, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: modes[0], RateMultiplier: 1},
				FallbackGroup: &service.Group{ID: 20, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: modes[1], RateMultiplier: 3},
			}
			ctx := service.WithAPIKeyGroupFallbackRouting(context.Background(), key)
			cache := &fallbackReservationCache{newHandlerInflightCache(10)}
			cfg := &config.Config{}
			cfg.Billing.InflightReservation = config.InflightReservationConfig{Enabled: true, TTLSeconds: 60}
			billing := service.NewBillingCacheService(cache, nil, fallbackReservationSubRepo{}, nil, nil, nil, cfg, nil)
			defer billing.Stop()
			var sub *service.UserSubscription
			if modes[0] == service.SubscriptionTypeSubscription {
				sub = &service.UserSubscription{ID: 110, GroupID: 10}
			}
			require.NoError(t, billing.CheckBillingEligibility(ctx, key.User, key, key.Group, sub, ""))
			ctx, done, err := reserveInflightBalanceCtx(ctx, billing, fallbackPriceEstimator{}, key, sub, tokenInflightEstimate("m", nil))
			require.NoError(t, err)
			defer done()
			repo := auditFallbackAccountRepo{strictRequestAccountRepo{openAIImagesFailoverAccountRepo{accounts: []service.Account{{ID: 6, GroupIDs: []int64{20}, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true}}}}}
			h := newOpenAIResponsesFailoverTestHandler(t, &strictRequestUpstream{winner: 6}, repo)
			h.cfg.RunMode = "standard"
			selected, err := h.gatewayService.SelectAccountWithLoadAwareness(ctx, key.GroupID, "", "m", nil)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.EqualValues(t, 20, *key.GroupID)
			if selected.ReleaseFunc != nil {
				defer selected.ReleaseFunc()
			}
			reservation := service.InflightReservationFromContext(ctx)
			if modes[1] == service.SubscriptionTypeSubscription {
				require.Nil(t, reservation)
				require.Zero(t, cache.count())
				return
			}
			require.InDelta(t, 3, reservation.Amount(), 0.001)
			require.Equal(t, 1, cache.count())
			task, abandon := wrapUsageRecordTaskContext(ctx, func(context.Context) {})
			done()
			require.Equal(t, 1, cache.count(), "usage task must retain target reservation")
			task(context.Background())
			abandon()
			require.Zero(t, cache.count())
			// A later WebSocket turn reserves against the already active group.
			next, nextDone, err := reserveInflightBalanceCtx(ctx, billing, fallbackPriceEstimator{}, key, nil, tokenInflightEstimate("m", nil))
			require.NoError(t, err)
			require.InDelta(t, 3, service.InflightReservationFromContext(next).Amount(), 0.001)
			nextDone()
			require.Zero(t, cache.count())
		})
	}
}
