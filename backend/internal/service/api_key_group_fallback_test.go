//go:build unit

package service

import (
	"context"
	"errors"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func apiKeyFallbackTestContext(primaryGroupID, fallbackGroupID int64) context.Context {
	primary := &Group{ID: primaryGroupID, Name: "primary", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	fallback := &Group{ID: fallbackGroupID, Name: "fallback", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	return WithAPIKeyGroupFallbackRouting(context.Background(), &APIKey{
		GroupID:         &primaryGroupID,
		FallbackGroupID: &fallbackGroupID,
		Group:           primary,
		FallbackGroup:   fallback,
	})
}

func newAPIKeyFallbackOpenAIService(accounts []Account) *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = false
	return &OpenAIGatewayService{
		accountRepo: schedulerGroupAwareOpenAIAccountRepo{
			schedulerTestOpenAIAccountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts},
		},
		cache:              &schedulerTestGatewayCache{},
		cfg:                cfg,
		concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{}),
	}
}

func TestAPIKeyGroupFallback_PrimaryAlwaysWins(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	primaryGroupID := int64(51001)
	fallbackGroupID := int64(51002)
	svc := newAPIKeyFallbackOpenAIService([]Account{
		{ID: 52001, GroupIDs: []int64{primaryGroupID}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1},
		{ID: 52002, GroupIDs: []int64{fallbackGroupID}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1},
	})

	selection, _, err := svc.SelectAccountWithScheduler(
		apiKeyFallbackTestContext(primaryGroupID, fallbackGroupID),
		&primaryGroupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, false,
	)

	require.NoError(t, err)
	require.Equal(t, int64(52001), selection.Account.ID)
	require.False(t, selection.UsedAPIKeyFallback)
	require.Zero(t, selection.RoutedGroupID)
}

func TestAPIKeyGroupFallback_UsesFallbackOnlyAfterPrimaryUnavailable(t *testing.T) {
	resetOpenAIAdvancedSchedulerSettingCacheForTest()
	primaryGroupID := int64(51011)
	fallbackGroupID := int64(51012)
	svc := newAPIKeyFallbackOpenAIService([]Account{
		{ID: 52012, GroupIDs: []int64{fallbackGroupID}, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1},
	})

	selection, _, err := svc.SelectAccountWithScheduler(
		apiKeyFallbackTestContext(primaryGroupID, fallbackGroupID),
		&primaryGroupID, "", "", "gpt-5.4", nil, OpenAIUpstreamTransportAny, false,
	)

	require.NoError(t, err)
	require.Equal(t, int64(52012), selection.Account.ID)
	require.True(t, selection.UsedAPIKeyFallback)
	require.Equal(t, fallbackGroupID, selection.RoutedGroupID)
}

func TestAPIKeyGroupFallback_SwitchesRequestBillingGroup(t *testing.T) {
	primaryGroupID := int64(51013)
	fallbackGroupID := int64(51014)
	override := 9
	primary := &Group{
		ID: primaryGroupID, Name: "primary", Platform: PlatformOpenAI,
		Status: StatusActive, Hydrated: true, RateMultiplier: 1,
	}
	fallback := &Group{
		ID: fallbackGroupID, Name: "fallback", Platform: PlatformOpenAI,
		Status: StatusActive, Hydrated: true, RateMultiplier: 2.5,
		PeakRateEnabled: true, PeakRateMultiplier: 1.4,
	}
	apiKey := &APIKey{
		GroupID:         &primaryGroupID,
		FallbackGroupID: &fallbackGroupID,
		Group:           primary,
		FallbackGroup:   fallback,
		User:            &User{ID: 77, UserGroupRPMOverride: &override},
	}
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), apiKey)

	account, err := selectAccountWithAPIKeyGroupFallback(ctx, &primaryGroupID, "gpt-test", func(_ context.Context, groupID *int64) (*Account, error) {
		if *groupID == primaryGroupID {
			return nil, ErrNoAvailableAccounts
		}
		return &Account{ID: 52014}, nil
	})

	require.NoError(t, err)
	require.Equal(t, int64(52014), account.ID)
	require.Equal(t, fallbackGroupID, *apiKey.GroupID)
	require.Same(t, fallback, apiKey.Group)
	require.Equal(t, 2.5, apiKey.Group.RateMultiplier)
	require.Nil(t, apiKey.User.UserGroupRPMOverride)
}

func TestAPIKeyGroupFallback_DoesNotRunForInfrastructureError(t *testing.T) {
	primaryGroupID := int64(51021)
	fallbackGroupID := int64(51022)
	ctx := apiKeyFallbackTestContext(primaryGroupID, fallbackGroupID)

	fallback, _, ok := apiKeyFallbackGroupForSelection(ctx, &primaryGroupID, errors.New("database unavailable"))

	require.False(t, ok)
	require.Nil(t, fallback)
}

func TestAPIKeyGroupFallback_AccountSelectionPreservesPrimaryFirst(t *testing.T) {
	primaryGroupID := int64(51023)
	fallbackGroupID := int64(51024)
	ctx := apiKeyFallbackTestContext(primaryGroupID, fallbackGroupID)
	var attempted []int64

	account, err := selectAccountWithAPIKeyGroupFallback(ctx, &primaryGroupID, "gpt-test", func(_ context.Context, groupID *int64) (*Account, error) {
		attempted = append(attempted, *groupID)
		if *groupID == primaryGroupID {
			return nil, ErrNoAvailableAccounts
		}
		return &Account{ID: 52024}, nil
	})

	require.NoError(t, err)
	require.Equal(t, int64(52024), account.ID)
	require.Equal(t, []int64{primaryGroupID, fallbackGroupID}, attempted)
}

func TestAPIKeyGroupFallback_RejectsInvalidConfiguration(t *testing.T) {
	primaryGroupID := int64(51031)
	fallbackGroupID := int64(51032)
	primary := &Group{ID: primaryGroupID, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}
	fallback := &Group{ID: fallbackGroupID, Platform: PlatformAnthropic, Status: StatusActive, Hydrated: true}
	ctx := WithAPIKeyGroupFallbackRouting(context.Background(), &APIKey{
		GroupID:         &primaryGroupID,
		FallbackGroupID: &fallbackGroupID,
		Group:           primary,
		FallbackGroup:   fallback,
	})

	selected, _, ok := apiKeyFallbackGroupForSelection(ctx, &primaryGroupID, ErrNoAvailableAccounts)

	require.False(t, ok)
	require.Nil(t, selected)
}
