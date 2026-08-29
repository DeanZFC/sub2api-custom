//go:build unit

package service

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAdminServiceCreateAccountRateLimit429RetryCount(t *testing.T) {
	tests := []struct {
		name  string
		value *int
		want  int
	}{
		{name: "defaults to five", want: DefaultRateLimit429RetryCount},
		{name: "explicit zero disables retries", value: retryCountPointer(0), want: 0},
		{name: "explicit maximum is accepted", value: retryCountPointer(MaxRateLimit429RetryCount), want: MaxRateLimit429RetryCount},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := &accountRepoStubForBulkUpdate{createID: 41}
			svc := &adminServiceImpl{accountRepo: repo}

			created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
				Name:                   "retry-account",
				Platform:               PlatformAnthropic,
				Type:                   AccountTypeAPIKey,
				RateLimit429RetryCount: tt.value,
				SkipDefaultGroupBind:   true,
			})

			require.NoError(t, err)
			require.Same(t, repo.createAccount, created)
			require.NotNil(t, created.RateLimit429RetryCount)
			require.Equal(t, tt.want, *created.RateLimit429RetryCount)
		})
	}
}

func TestAdminServiceCreateAccountRejectsOutOfRangeRateLimit429RetryCount(t *testing.T) {
	for _, value := range []int{-1, MaxRateLimit429RetryCount + 1} {
		t.Run(retryCountTestName(value), func(t *testing.T) {
			repo := &accountRepoStubForBulkUpdate{}
			svc := &adminServiceImpl{accountRepo: repo}

			created, err := svc.CreateAccount(context.Background(), &CreateAccountInput{
				Platform:               PlatformAnthropic,
				Type:                   AccountTypeAPIKey,
				RateLimit429RetryCount: retryCountPointer(value),
				SkipDefaultGroupBind:   true,
			})

			require.Nil(t, created)
			require.ErrorContains(t, err, "rate_limit_429_retry_count must be between 0 and 10")
			require.Nil(t, repo.createAccount, "invalid input must be rejected before persistence")
		})
	}
}

func TestAdminServiceUpdateAccountRateLimit429RetryCount(t *testing.T) {
	t.Run("nil preserves the current value", func(t *testing.T) {
		current := 3
		repo := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{
			7: {
				ID:                     7,
				Platform:               PlatformAnthropic,
				Type:                   AccountTypeAPIKey,
				Status:                 StatusActive,
				RateLimit429RetryCount: retryCountPointer(current),
			},
		}}

		updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(
			context.Background(),
			7,
			&UpdateAccountInput{Name: "renamed"},
		)

		require.NoError(t, err)
		require.NotNil(t, updated.RateLimit429RetryCount)
		require.Equal(t, current, *updated.RateLimit429RetryCount)
		require.Len(t, repo.updatedAccounts, 1)
	})

	for _, value := range []int{0, MaxRateLimit429RetryCount} {
		t.Run("explicit "+retryCountTestName(value), func(t *testing.T) {
			repo := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{
				7: {
					ID:                     7,
					Platform:               PlatformAnthropic,
					Type:                   AccountTypeAPIKey,
					Status:                 StatusActive,
					RateLimit429RetryCount: retryCountPointer(3),
				},
			}}

			updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(
				context.Background(),
				7,
				&UpdateAccountInput{RateLimit429RetryCount: retryCountPointer(value)},
			)

			require.NoError(t, err)
			require.NotNil(t, updated.RateLimit429RetryCount)
			require.Equal(t, value, *updated.RateLimit429RetryCount)
			require.Len(t, repo.updatedAccounts, 1)
		})
	}
}

func TestAdminServiceUpdateAccountRejectsOutOfRangeRateLimit429RetryCount(t *testing.T) {
	for _, value := range []int{-1, MaxRateLimit429RetryCount + 1} {
		t.Run(retryCountTestName(value), func(t *testing.T) {
			repo := &accountRepoStubForBulkUpdate{getByIDAccounts: map[int64]*Account{
				7: {
					ID:                     7,
					Platform:               PlatformAnthropic,
					Type:                   AccountTypeAPIKey,
					Status:                 StatusActive,
					RateLimit429RetryCount: retryCountPointer(3),
				},
			}}

			updated, err := (&adminServiceImpl{accountRepo: repo}).UpdateAccount(
				context.Background(),
				7,
				&UpdateAccountInput{RateLimit429RetryCount: retryCountPointer(value)},
			)

			require.Nil(t, updated)
			require.ErrorContains(t, err, "rate_limit_429_retry_count must be between 0 and 10")
			require.Empty(t, repo.updatedAccounts, "invalid input must be rejected before persistence")
		})
	}
}

func TestAdminServiceBulkUpdateAccountsRateLimit429RetryCount(t *testing.T) {
	for _, value := range []int{0, MaxRateLimit429RetryCount} {
		t.Run(retryCountTestName(value), func(t *testing.T) {
			repo := &accountRepoStubForBulkUpdate{}

			result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(
				context.Background(),
				&BulkUpdateAccountsInput{
					AccountIDs:             []int64{11, 12},
					RateLimit429RetryCount: retryCountPointer(value),
				},
			)

			require.NoError(t, err)
			require.Equal(t, 2, result.Success)
			require.Equal(t, 1, repo.bulkUpdateCalls)
			require.NotNil(t, repo.lastBulkUpdate.RateLimit429RetryCount)
			require.Equal(t, value, *repo.lastBulkUpdate.RateLimit429RetryCount)
		})
	}
}

func TestAdminServiceBulkUpdateAccountsRejectsOutOfRangeRateLimit429RetryCount(t *testing.T) {
	for _, value := range []int{-1, MaxRateLimit429RetryCount + 1} {
		t.Run(retryCountTestName(value), func(t *testing.T) {
			repo := &accountRepoStubForBulkUpdate{}

			result, err := (&adminServiceImpl{accountRepo: repo}).BulkUpdateAccounts(
				context.Background(),
				&BulkUpdateAccountsInput{
					AccountIDs:             []int64{11},
					RateLimit429RetryCount: retryCountPointer(value),
				},
			)

			require.Nil(t, result)
			require.ErrorContains(t, err, "rate_limit_429_retry_count must be between 0 and 10")
			require.Zero(t, repo.bulkUpdateCalls, "invalid input must be rejected before persistence")
		})
	}
}

func retryCountTestName(value int) string {
	return "value_" + strconv.Itoa(value)
}
