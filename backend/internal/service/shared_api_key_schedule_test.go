package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestApplySharedKeyScheduleRatePrefersCheaperAccounts(t *testing.T) {
	high, low := 2.0, 0.5
	accounts := []Account{
		{ID: 1, RateMultiplier: &high},
		{ID: 2, RateMultiplier: &low},
		{ID: 3},
	}
	ctx := WithSharedKeySchedule(context.Background(), SharedKeySchedule{Priority: SharedKeyPriorityRate})
	got := applySharedKeySchedule(ctx, accounts)
	require.Equal(t, []int64{2, 3, 1}, []int64{got[0].ID, got[1].ID, got[2].ID})
}

func TestApplySharedKeyScheduleAvailabilityPrefersIdleAccounts(t *testing.T) {
	busyUntil := time.Now().Add(time.Hour)
	old := time.Now().Add(-2 * time.Hour)
	recent := time.Now().Add(-time.Minute)
	accounts := []Account{
		{ID: 1, LastUsedAt: &recent, RateLimitResetAt: &busyUntil},
		{ID: 2, LastUsedAt: &old},
		{ID: 3, LastUsedAt: &recent},
	}
	ctx := WithSharedKeySchedule(context.Background(), SharedKeySchedule{Priority: SharedKeyPriorityAvailability})
	got := applySharedKeySchedule(ctx, accounts)
	require.Equal(t, int64(2), got[0].ID)
	require.Equal(t, int64(1), got[len(got)-1].ID)
}

func TestApplySharedKeyScheduleManualOrderKeepsConfiguredSequence(t *testing.T) {
	accounts := []Account{{ID: 3}, {ID: 1}, {ID: 2}, {ID: 9}}
	ctx := WithSharedListingOrder(context.Background(), []int64{2, 1})
	got := applySharedKeySchedule(ctx, accounts)
	require.Equal(t, []int64{2, 1}, []int64{got[0].ID, got[1].ID})
	require.Len(t, got, 2, "accounts outside the shared key selection must not be scheduled")
}

func TestNormalizeSharedKeyModesPlatformDefaultsRate(t *testing.T) {
	selection, priority, err := normalizeSharedKeyModes("platform", "order")
	require.NoError(t, err)
	require.Equal(t, SharedKeySelectionPlatform, selection)
	require.Equal(t, SharedKeyPriorityRate, priority)
}
