package service

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestResolveBillingServiceTier(t *testing.T) {
	for _, requested := range []string{"", "default", "standard", "auto", "scale", "flex", "priority", "fast", "ultrafast", " Priority ", "unknown-request"} {
		for _, observed := range []string{"", "default", "standard", "auto", "scale", "flex", "priority", "fast", "ultrafast", " DEFAULT ", "unknown-response"} {
			t.Run(requested+"/"+observed, func(t *testing.T) {
				got := ResolveBillingServiceTier(requested, observed)
				require.Equal(t, normalizeBillingServiceTier(requested), got.Requested)
				require.Equal(t, normalizeBillingServiceTier(observed), got.Observed)
				require.Equal(t, normalizeBillingServiceTier(requested), got.Billing)
				require.False(t, got.Downgraded)
			})
		}
	}
}

func TestResolveOpenAIServiceTierBilling_AllAccountsUseOutboundTier(t *testing.T) {
	accounts := []*Account{
		nil,
		{Platform: PlatformOpenAI, Type: AccountTypeAPIKey},
		{Platform: PlatformOpenAI, Type: AccountTypeOAuth},
		{Platform: PlatformOpenAI, Type: AccountTypeSetupToken},
		{Platform: PlatformGrok, Type: AccountTypeOAuth},
		{Platform: PlatformAnthropic, Type: AccountTypeAPIKey},
	}
	for _, account := range accounts {
		name := "nil"
		if account != nil {
			name = account.Platform + "/" + account.Type
		}
		for _, requested := range []string{"", "default", "flex", "priority", "fast", "ultrafast"} {
			for _, observed := range []string{"", "default", "standard", "flex", "priority", "ultrafast"} {
				t.Run(name+"/"+requested+"/"+observed, func(t *testing.T) {
					require.Equal(t, ResolveBillingServiceTier(requested, observed), ResolveOpenAIServiceTierBilling(account, requested, observed))
				})
			}
		}
	}
}

func TestApplyServiceTierBillingResolution_PreservesRequestAndResponse(t *testing.T) {
	for _, requested := range []string{"default", "flex", "priority", "fast", "ultrafast", " Priority "} {
		for _, observed := range []string{"", "default", "standard", "flex", "priority", "ultrafast", "unknown-response"} {
			t.Run(requested+"/"+observed, func(t *testing.T) {
				openAIResult := &OpenAIForwardResult{ServiceTier: &requested, UpstreamResponseServiceTier: observed}
				forwardResult := &ForwardResult{ServiceTier: &requested, UpstreamResponseServiceTier: observed}
				want := ResolveBillingServiceTier(requested, observed)
				require.Equal(t, want, ApplyOpenAIServiceTierBillingResolution(&Account{Platform: PlatformOpenAI, Type: AccountTypeAPIKey}, openAIResult))
				require.Equal(t, want, ApplyForwardServiceTierBillingResolution(forwardResult))
				require.Same(t, &requested, openAIResult.ServiceTier)
				require.Same(t, &requested, forwardResult.ServiceTier)
				require.Equal(t, requested, *openAIResult.ServiceTier)
				require.Equal(t, requested, *forwardResult.ServiceTier)
				require.Equal(t, observed, openAIResult.UpstreamResponseServiceTier)
				require.Equal(t, observed, forwardResult.UpstreamResponseServiceTier)
			})
		}
	}

	t.Run("no request tier stays nil despite response tier", func(t *testing.T) {
		openAIResult := &OpenAIForwardResult{UpstreamResponseServiceTier: "ultrafast"}
		forwardResult := &ForwardResult{UpstreamResponseServiceTier: "fast"}
		require.Empty(t, ApplyOpenAIServiceTierBillingResolution(nil, openAIResult).Billing)
		require.Empty(t, ApplyForwardServiceTierBillingResolution(forwardResult).Billing)
		require.Nil(t, openAIResult.ServiceTier)
		require.Nil(t, forwardResult.ServiceTier)
	})

	t.Run("nil results are ignored", func(t *testing.T) {
		require.Equal(t, ServiceTierBillingResolution{}, ApplyOpenAIServiceTierBillingResolution(nil, nil))
		require.Equal(t, ServiceTierBillingResolution{}, ApplyForwardServiceTierBillingResolution(nil))
	})
}
