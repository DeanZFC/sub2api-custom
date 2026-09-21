package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func billingLimitDeliveryAccount(id int64, rate, limit float64) *Account {
	a := upstreamCostTestAccount(id, UpstreamBillingProbeStatusOK, rate, time.Now(), 30*time.Minute)
	a.Status = StatusActive
	a.Schedulable = true
	a.Concurrency = 1
	a.Extra[UpstreamBillingRateLimitExtraKey] = limit
	return a
}

func TestUpstreamBillingRateLimitStopsSchedulerBeforeAcquiringSlot(t *testing.T) {
	for _, enabled := range []string{"false", "true"} {
		t.Run("advanced="+enabled, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			blocked := billingLimitDeliveryAccount(1, 2, 1)
			allowed := billingLimitDeliveryAccount(2, 3, 3)
			allowed.Priority = 10
			cache := &upstreamCostTrackingConcurrencyCache{}
			svc := &OpenAIGatewayService{
				accountRepo:        schedulerTestOpenAIAccountRepo{accounts: []Account{*blocked, *allowed}},
				cfg:                &config.Config{},
				rateLimitService:   newOpenAIAdvancedSchedulerRateLimitService(enabled),
				concurrencyService: NewConcurrencyService(cache),
			}
			groupID := int64(1)
			selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "gpt-test", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, allowed.ID, selection.Account.ID)
			require.Empty(t, cache.limits(blocked.ID))
			selection.ReleaseFunc()
		})
	}
}

func TestUpstreamBillingRateLimitRechecksHydratedAccount(t *testing.T) {
	ctx := context.Background()
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini} {
		t.Run(platform, func(t *testing.T) {
			stale := billingLimitDeliveryAccount(1, 1, 1)
			fresh := billingLimitDeliveryAccount(1, 2, 1)
			stale.Platform, fresh.Platform = platform, platform
			snapshot := NewSchedulerSnapshotService(&openAISnapshotCacheStub{accountsByID: map[int64]*Account{1: fresh}}, nil, nil, nil, nil)
			var account *Account
			var err error
			switch platform {
			case PlatformOpenAI:
				svc := &OpenAIGatewayService{schedulerSnapshot: snapshot}
				released := 0
				selection, selectionErr := svc.newAcquiredSelectionResult(ctx, stale, func() { released++ })
				require.ErrorIs(t, selectionErr, ErrNoAvailableAccounts)
				require.Nil(t, selection)
				require.Equal(t, 1, released)
				account, err = svc.hydrateSelectedAccount(ctx, stale)
			case PlatformAnthropic:
				svc := &GatewayService{schedulerSnapshot: snapshot}
				released := 0
				selection, selectionErr := svc.newSelectionResult(ctx, stale, true, func() { released++ }, nil)
				require.ErrorIs(t, selectionErr, ErrNoAvailableAccounts)
				require.Nil(t, selection)
				require.Equal(t, 1, released)
				account, err = svc.hydrateSelectedAccount(ctx, stale)
			case PlatformGemini:
				account, err = (&GeminiMessagesCompatService{schedulerSnapshot: snapshot}).hydrateSelectedAccount(ctx, stale)
			}
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, account)
		})
	}
}

type billingLimitOpsAccountRepo struct {
	AccountRepository
	accounts []Account
}

func (r billingLimitOpsAccountRepo) ListOpsAccountsForStats(context.Context, string, *int64) ([]Account, error) {
	return r.accounts, nil
}

func TestUpstreamBillingRateLimitExcludedFromOpsAvailability(t *testing.T) {
	blocked := billingLimitDeliveryAccount(1, 2, 1)
	allowed := billingLimitDeliveryAccount(2, 1, 1)
	svc := &OpsService{accountRepo: billingLimitOpsAccountRepo{accounts: []Account{*blocked, *allowed}}}
	platforms, _, _, _, err := svc.GetAccountAvailabilityStats(context.Background(), "", nil)
	require.NoError(t, err)
	require.EqualValues(t, 2, platforms[PlatformOpenAI].TotalAccounts)
	require.EqualValues(t, 1, platforms[PlatformOpenAI].AvailableCount)
}
