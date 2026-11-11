package service

// ServiceTierBillingResolution keeps the final outbound request tier separate
// from the tier observed in the upstream response. Only the request tier sets
// the billing tier; the upstream declaration remains diagnostic information.
type ServiceTierBillingResolution struct {
	Requested  string // tier carried by the request sent upstream ("" when none)
	Observed   string // tier declared by the upstream response ("" when none)
	Billing    string // normalized outbound request tier used for billing
	Downgraded bool   // retained for compatibility; responses never change billing
}

// ResolveBillingServiceTier always bills the final outbound request tier.
// Upstream responses cannot lower or raise it, including when the request had
// no tier after request filtering or other policies had run.
func ResolveBillingServiceTier(requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	return ServiceTierBillingResolution{
		Requested: requested,
		Observed:  normalizeBillingServiceTier(observed),
		Billing:   requested,
	}
}

// ResolveOpenAIServiceTierBilling preserves the same request-tier contract for
// every account and credential type. The account argument remains for callers
// of this compatibility entry point.
func ResolveOpenAIServiceTierBilling(_ *Account, requested, observed string) ServiceTierBillingResolution {
	return ResolveBillingServiceTier(requested, observed)
}

// ApplyOpenAIServiceTierBillingResolution reports the billing resolution
// without changing the final outbound tier or the observed response tier.
func ApplyOpenAIServiceTierBillingResolution(account *Account, result *OpenAIForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	return ResolveOpenAIServiceTierBilling(account, optionalStringValue(result.ServiceTier), result.UpstreamResponseServiceTier)
}

// ApplyForwardServiceTierBillingResolution is the ForwardResult counterpart of
// ApplyOpenAIServiceTierBillingResolution.
func ApplyForwardServiceTierBillingResolution(result *ForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	return ResolveBillingServiceTier(optionalStringValue(result.ServiceTier), result.UpstreamResponseServiceTier)
}
