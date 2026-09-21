//go:build unit

package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestQualityPausedAccountNeverSchedules(t *testing.T) {
	account := &Account{Status: StatusQualityPaused, Schedulable: true}
	require.False(t, account.IsActive())
	require.False(t, account.IsSchedulable())

	account.Schedulable = false
	account.Status = StatusActive
	require.False(t, account.IsSchedulable(), "restoring quality must not override a manual stop")
}

func TestRefreshIfNeededQualityPausedPreservesSchedulingState(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformGrok} {
		for _, schedulable := range []bool{true, false} {
			t.Run(platform+map[bool]string{true: "/auto-paused", false: "/manually-stopped"}[schedulable], func(t *testing.T) {
				account := &Account{
					ID: 88, Platform: platform, Type: AccountTypeOAuth,
					Status: StatusQualityPaused, Schedulable: schedulable,
					Credentials: map[string]any{"access_token": "old-access", "refresh_token": "refresh"},
					Extra:       map[string]any{"quality_protection_reason": "quality below threshold"},
				}
				repo := &refreshAPIAccountRepo{account: account}
				executor := &refreshAPIExecutorStub{
					needsRefresh: true,
					credentials:  map[string]any{"access_token": "new-access", "refresh_token": "new-refresh"},
				}
				result, err := NewOAuthRefreshAPI(repo, nil).RefreshIfNeeded(context.Background(), account, executor, time.Hour)
				require.NoError(t, err)
				require.True(t, result.Refreshed)
				require.Equal(t, 1, executor.refreshCalls)
				require.Equal(t, "new-access", result.Account.GetCredential("access_token"))
				require.Equal(t, StatusQualityPaused, result.Account.Status)
				require.Equal(t, schedulable, result.Account.Schedulable)
				require.Equal(t, "quality below threshold", result.Account.Extra["quality_protection_reason"])
				require.False(t, result.Account.IsSchedulable())
			})
		}
	}
}

func TestRefreshIfNeededQualityPausedRejectsRequestPath(t *testing.T) {
	account := &Account{ID: 89, Platform: PlatformGrok, Type: AccountTypeOAuth, Status: StatusQualityPaused, Schedulable: true}
	repo := &refreshAPIAccountRepo{account: account}
	executor := &refreshAPIExecutorStub{needsRefresh: true}
	result, err := NewOAuthRefreshAPI(repo, nil).RefreshIfNeeded(withOAuthRefreshRequestPath(context.Background()), account, executor, time.Hour)
	require.ErrorIs(t, err, errOAuthRefreshAccountStateChanged)
	require.Nil(t, result)
	require.Zero(t, executor.refreshCalls)
}
