package dto

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestAccountUpstreamBillingRateLimitVisibleWithoutChangingManualScheduling(t *testing.T) {
	account := &service.Account{
		ID: 42, Platform: service.PlatformOpenAI, Type: service.AccountTypeAPIKey,
		Status: service.StatusActive, Schedulable: true,
		Extra: map[string]any{
			service.UpstreamBillingRateLimitExtraKey: 1.0,
			service.UpstreamBillingProbeExtraKey: map[string]any{
				"status": service.UpstreamBillingProbeStatusFailed,
				"data": map[string]any{
					"billing_scope": "token", "resolved_rate_multiplier": 2.0,
					"peak_rate_enabled": false,
				},
			},
		},
	}
	detail := AccountFromServiceShallow(account)
	compact := AccountListItemFromAccount(detail)
	require.True(t, detail.UpstreamBillingRateLimited)
	require.True(t, compact.UpstreamBillingRateLimited)
	require.True(t, detail.Schedulable, "automatic protection must preserve the manual switch")
	require.True(t, account.Schedulable)
	for _, payload := range []any{detail, compact} {
		data, err := json.Marshal(payload)
		require.NoError(t, err)
		require.Contains(t, string(data), `"upstream_billing_rate_limited":true`)
	}

	account.Extra[service.UpstreamBillingRateLimitExtraKey] = nil
	detail = AccountFromServiceShallow(account)
	require.False(t, detail.UpstreamBillingRateLimited)
	require.True(t, detail.Schedulable)
}
