//go:build unit

package service

import (
	"context"
	"fmt"
	"net/http"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/stretchr/testify/require"
)

func TestHandleUpstreamError_ProviderCreditsUseRecoverableWalletLimits(t *testing.T) {
	for _, platform := range []string{PlatformCommandCode, PlatformCline} {
		for _, statusCode := range []int{http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests} {
			t.Run(fmt.Sprintf("%s/%d", platform, statusCode), func(t *testing.T) {
				repo := &commandCodeRateLimitRepo{}
				account := commandCodeUsageAccount()
				if platform == PlatformCline {
					account = clineTestAccount(78)
				}
				body := []byte(`{"error":{"code":"insufficient_credits","message":"Insufficient credits"}}`)
				shouldDisable := NewRateLimitService(repo, nil, &config.Config{}, nil, nil).HandleUpstreamError(
					context.Background(), account, statusCode, http.Header{}, body, "deepseek/deepseek-v4-flash")

				require.Zero(t, repo.setErrorCalls, "recoverable credits must not permanently disable the account")
				require.Zero(t, repo.rateLimitedCalls)
				if platform == PlatformCline {
					require.False(t, shouldDisable, "the independent ClinePass wallet remains usable")
					require.Zero(t, repo.tempCalls)
					require.Len(t, repo.modelLimits, 1)
					require.Contains(t, repo.modelLimits, clineCreditsRateLimitKey)
					require.Equal(t, clineCreditsReason+": Insufficient credits", repo.reasons[clineCreditsRateLimitKey])
				} else {
					require.Equal(t, statusCode != http.StatusTooManyRequests, shouldDisable)
					require.Equal(t, 1, repo.tempCalls)
					require.Contains(t, repo.lastTempReason, cnBalanceLowReasonPrefix)
					require.Equal(t, true, repo.lastExtraUpdates["command_code_balance_low"])
				}
			})
		}
	}
}

func TestHandleUpstreamError_OrdinaryBillingErrorsStillDisableAccount(t *testing.T) {
	for _, statusCode := range []int{http.StatusBadRequest, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(statusCode), func(t *testing.T) {
			repo := &rateLimitAccountRepoStub{}
			account := &Account{ID: 79, Platform: PlatformOpenAI, Type: AccountTypeAPIKey}
			body := []byte(`{"error":{"code":"insufficient_credits","message":"Insufficient credits"}}`)
			shouldDisable := NewRateLimitService(repo, nil, &config.Config{}, nil, nil).HandleUpstreamError(
				context.Background(), account, statusCode, http.Header{}, body)

			require.True(t, shouldDisable)
			require.Equal(t, 1, repo.setErrorCalls)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.rateLimitedCalls)
		})
	}
}

func TestHandleUpstreamError_ClineCreditCooldownWriteFailureDoesNotDisableAccount(t *testing.T) {
	for _, statusCode := range []int{http.StatusPaymentRequired, http.StatusForbidden, http.StatusTooManyRequests} {
		t.Run(fmt.Sprint(statusCode), func(t *testing.T) {
			repo := &clineModelLimitFailRepo{}
			account := clineTestAccount(80)
			body := []byte(`{"error":{"code":"insufficient_credits","message":"Insufficient credits"}}`)
			shouldDisable := NewRateLimitService(repo, nil, &config.Config{}, nil, nil).HandleUpstreamError(
				context.Background(), account, statusCode, http.Header{}, body, "deepseek/deepseek-v4-flash")

			require.False(t, shouldDisable)
			require.Zero(t, repo.setErrorCalls)
			require.Zero(t, repo.tempCalls)
			require.Zero(t, repo.rateLimitedCalls)
			require.True(t, account.isRateLimitActiveForKey(clineCreditsRateLimitKey))
		})
	}
}
