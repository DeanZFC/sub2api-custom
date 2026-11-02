//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

type fallbackAdmissionCache struct {
	BillingCache
	balance float64
	expires time.Time
}

func (c *fallbackAdmissionCache) GetUserBalance(context.Context, int64) (float64, error) {
	return c.balance, nil
}
func (c *fallbackAdmissionCache) GetSubscriptionCache(context.Context, int64, int64) (*SubscriptionCacheData, error) {
	return &SubscriptionCacheData{Status: SubscriptionStatusActive, ExpiresAt: c.expires}, nil
}

type fallbackSlotCache struct {
	ConcurrencyCache
	allow              bool
	from, to, released int64
	moves, releases    int
}

func (c *fallbackSlotCache) AcquireUserGroupSlot(context.Context, int64, int64, int, int, string) (bool, error) {
	return true, nil
}
func (c *fallbackSlotCache) ReleaseUserGroupSlot(_ context.Context, _, group int64, _ string) error {
	c.released = group
	c.releases++
	return nil
}
func (c *fallbackSlotCache) MoveUserGroupSlot(_ context.Context, _, from, to int64, _, _ int, _ string) (bool, error) {
	c.from, c.to = from, to
	c.moves++
	return c.allow, nil
}

func TestAPIKeyFallbackSubscriptionAdmissionAndSnapshot(t *testing.T) {
	for _, state := range []string{"missing", "expired", "active"} {
		t.Run(state, func(t *testing.T) {
			ctx := apiKeyFallbackTestContext(10, 20)
			key := fallbackRouting(ctx).apiKey
			key.User = &User{ID: 7}
			key.Group.SubscriptionType = SubscriptionTypeSubscription
			key.FallbackGroup.SubscriptionType = SubscriptionTypeSubscription
			repo := &openAIRecordUsageSubRepoStub{}
			expiry := time.Now().Add(time.Hour)
			if state != "missing" {
				repo.active = &UserSubscription{ID: 200, UserID: 7, GroupID: 20, Status: SubscriptionStatusActive, ExpiresAt: expiry}
			}
			cache := &fallbackAdmissionCache{balance: 10, expires: expiry}
			billing := NewBillingCacheService(cache, nil, repo, nil, nil, nil, &config.Config{}, nil)
			defer billing.Stop()
			require.NoError(t, billing.CheckBillingEligibility(ctx, key.User, key, key.Group, &UserSubscription{ID: 100, GroupID: 10}, ""))
			if state == "expired" {
				cache.expires = time.Now().Add(-time.Hour)
			}
			err := admitAPIKeyFallback(ctx)
			if state != "active" {
				require.ErrorIs(t, err, ErrSubscriptionInvalid)
				require.EqualValues(t, 10, *key.GroupID)
				return
			}
			require.NoError(t, err)
			require.True(t, activateAPIKeyFallbackRouting(ctx, 20))
			snapshot := CopyAPIKeyFallbackSubscription(ctx, context.Background())
			repo.active = nil
			sub, err := resolveUsageSubscription(snapshot, repo, key, key.User, &UserSubscription{ID: 100, GroupID: 10})
			require.NoError(t, err)
			require.EqualValues(t, 200, sub.ID)
			require.Equal(t, 1, repo.getActiveCalls)
		})
	}
}

func TestAPIKeyFallbackCountsTargetRPMOnceAndReleasesAccountOnDenial(t *testing.T) {
	ctx := apiKeyFallbackTestContext(10, 20)
	key := fallbackRouting(ctx).apiKey
	key.User = &User{ID: 7, RPMLimit: 1}
	key.Group.RPMLimit = 10
	key.FallbackGroup.RPMLimit = 1
	cache := &fallbackAdmissionCache{balance: 10}
	rpm := &userRPMCacheStub{userGroupCounts: []int{1, 2}}
	billing := NewBillingCacheService(cache, nil, nil, nil, rpm, nil, &config.Config{}, nil)
	defer billing.Stop()
	require.NoError(t, billing.CheckBillingEligibility(ctx, key.User, key, key.Group, nil, ""))
	released := 0
	value, err := selectWithGroupFallback(ctx, key.GroupID, "m", nil, func(_ context.Context, id *int64) (*AccountSelectionResult, error) {
		if *id == 10 {
			return nil, ErrNoAvailableAccounts
		}
		return &AccountSelectionResult{Account: &Account{ID: 99}, ReleaseFunc: func() { released++ }}, nil
	}, markGroupFallbackSelection)
	require.ErrorIs(t, err, ErrGroupRPMExceeded)
	require.Nil(t, value)
	require.Equal(t, 1, released)
	require.EqualValues(t, 1, rpm.userCalls)
	require.EqualValues(t, 2, rpm.userGroupCalls)
	require.EqualValues(t, 10, *key.GroupID)
}

func TestAPIKeyFallbackTransfersAndReleasesEffectiveGroup(t *testing.T) {
	for _, allow := range []bool{true, false} {
		t.Run(map[bool]string{true: "admitted", false: "full"}[allow], func(t *testing.T) {
			ctx := apiKeyFallbackTestContext(10, 20)
			key := fallbackRouting(ctx).apiKey
			key.FallbackGroup.UserConcurrencyLimit = 1
			cache := &fallbackSlotCache{allow: allow}
			svc := NewConcurrencyService(cache)
			slot, err := svc.AcquireUserGroupSlot(ctx, 7, 10, 3, 5)
			require.NoError(t, err)
			err = admitAPIKeyFallback(ctx)
			if allow {
				require.NoError(t, err)
				require.True(t, activateAPIKeyFallbackRouting(ctx, 20))
			} else {
				require.ErrorIs(t, err, ErrFallbackGroupConcurrency)
			}
			require.EqualValues(t, 10, cache.from)
			require.EqualValues(t, 20, cache.to)
			slot.ReleaseFunc()
			slot.ReleaseFunc()
			require.Equal(t, 1, cache.releases)
			want := int64(10)
			if allow {
				want = 20
			}
			require.Equal(t, want, cache.released)
		})
	}
}
