//go:build unit

package repository

import (
	"encoding/json"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestSchedulerCodexTicketPolicyProjectionSurvivesCacheRoundTrip(t *testing.T) {
	account := service.Account{
		ID: 41, Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth,
		ProxyIDs:    []int64{17, 19},
		Credentials: map[string]any{"chatgpt_plan_type": "team"},
		Extra: map[string]any{
			service.OpenAICodexTicketEnabledExtraKey:         false,
			service.OpenAICodexTicketFailClosedExtraKey:      false,
			service.OpenAICodexTicketHarvestProxyIDsExtraKey: []int64{8, 2},
			"private-unrelated":                              "omit",
		},
	}
	_, payload, err := marshalSchedulerCacheAccount(account)
	require.NoError(t, err)
	var cached service.Account
	require.NoError(t, json.Unmarshal(payload, &cached))
	require.Equal(t, false, cached.Extra[service.OpenAICodexTicketEnabledExtraKey])
	require.Equal(t, false, cached.Extra[service.OpenAICodexTicketFailClosedExtraKey])
	require.NotContains(t, cached.Extra, service.OpenAICodexTicketHarvestProxyIDsExtraKey)
	require.Equal(t, []int64{17, 19}, cached.ProxyIDs)
	require.Equal(t, "team", cached.Credentials["chatgpt_plan_type"])
	require.Equal(t, 332, service.ResolveOpenAICodexTicketAccountConfig(&cached, config.OpenAICodexTicketConfig{}).TargetLength)
	require.NotContains(t, cached.Extra, "private-unrelated")

}
