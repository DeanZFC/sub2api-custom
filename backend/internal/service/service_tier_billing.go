package service

import (
	"log/slog"
	"strings"
)

// ServiceTierBillingResolution describes how the billable service tier of one
// request was settled between the tier the client asked for and the tier the
// upstream reports having used.
type ServiceTierBillingResolution struct {
	Requested  string // tier carried by the request sent upstream ("" when none)
	Observed   string // tier declared by the upstream response ("" when none)
	Billing    string // tier used for billing and the usage log
	Downgraded bool   // Billing is cheaper than Requested
}

// ResolveBillingServiceTier applies the generic response-tier contract. The
// upstream declaration is trusted only to lower the bill: a request served below
// the tier it asked for (e.g. Anthropic speed=fast answered with usage.speed
// "standard") is billed at the cheaper tier. OpenAI Fast uses the specialized
// contract in ResolveOpenAIServiceTierBilling instead. A response
// that claims a more expensive tier, an unknown one, or none at all leaves the
// requested tier in place, which is also the behaviour for upstreams that never
// declare a tier.
func ResolveBillingServiceTier(requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	observed = normalizeBillingServiceTier(observed)
	resolution := ServiceTierBillingResolution{Requested: requested, Observed: observed, Billing: requested}
	if observed == "" || observed == requested {
		return resolution
	}
	observedRank, known := serviceTierCostRank(observed)
	if !known {
		return resolution
	}
	requestedRank, _ := serviceTierCostRank(requested)
	if observedRank >= requestedRank {
		return resolution
	}
	resolution.Billing = observed
	resolution.Downgraded = true
	return resolution
}

// serviceTierCostRank orders tiers by their cost relative to the base rate, so a
// lower rank is always cheaper. Unknown tiers rank as the base rate and report
// known=false so callers can refuse to act on them.
func serviceTierCostRank(tier string) (rank int, known bool) {
	switch normalizeBillingServiceTier(tier) {
	case "flex":
		return 0, true
	case "", "default", "standard", "auto", "scale":
		return 1, true
	case "priority", "fast":
		return 2, true
	case "ultrafast":
		return 3, true
	default:
		return 1, false
	}
}

// ResolveOpenAIServiceTierBilling bills OpenAI Fast at the final outbound
// request tier for every credential type, including API-key relays. A relay may
// charge Fast while returning default (or another tier), so its declaration
// must not lower the requested Fast price. Request policies have already run:
// a filtered Fast tier is absent here and cannot be restored by the response.
// Other tiers retain the existing response contract, including the private
// Codex OAuth default-echo exception. The observed tier stays separate.
func ResolveOpenAIServiceTierBilling(account *Account, requested, observed string) ServiceTierBillingResolution {
	requested = normalizeBillingServiceTier(requested)
	observed = normalizeBillingServiceTier(observed)
	if account != nil && account.IsOpenAI() && (requested == OpenAIFastTierPriority || requested == "fast") {
		return ServiceTierBillingResolution{Requested: requested, Observed: observed, Billing: requested}
	}
	if account != nil && account.IsOpenAIOAuthLike() && codexOAuthResponseTierIsNonAuthoritative(observed) {
		return ServiceTierBillingResolution{
			Requested: requested,
			Observed:  observed,
			Billing:   requested,
		}
	}
	return ResolveBillingServiceTier(requested, observed)
}

func codexOAuthResponseTierIsNonAuthoritative(observed string) bool {
	switch normalizeBillingServiceTier(observed) {
	case "default":
		return true
	default:
		return false
	}
}

// ApplyOpenAIServiceTierBillingResolution preserves outbound OpenAI Fast and
// otherwise lowers result.ServiceTier when the response contract permits it.
func ApplyOpenAIServiceTierBillingResolution(account *Account, result *OpenAIForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	resolution := ResolveOpenAIServiceTierBilling(account, optionalStringValue(result.ServiceTier), result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
	}
	return resolution
}

// ApplyForwardServiceTierBillingResolution is the ForwardResult counterpart of
// ApplyOpenAIServiceTierBillingResolution.
func ApplyForwardServiceTierBillingResolution(result *ForwardResult) ServiceTierBillingResolution {
	if result == nil {
		return ServiceTierBillingResolution{}
	}
	resolution := ResolveBillingServiceTier(optionalStringValue(result.ServiceTier), result.UpstreamResponseServiceTier)
	if resolution.Downgraded {
		billing := resolution.Billing
		result.ServiceTier = &billing
	}
	return resolution
}

// logServiceTierBillingDowngrade leaves an audit trail for every request billed
// below the tier it asked for; unchanged tiers are not logged.
func logServiceTierBillingDowngrade(component string, account *Account, requestID string, resolution ServiceTierBillingResolution) {
	if !resolution.Downgraded {
		return
	}
	attrs := []any{
		"component", component,
		"request_id", strings.TrimSpace(requestID),
		"requested_tier", resolution.Requested,
		"response_tier", resolution.Observed,
		"billed_tier", resolution.Billing,
	}
	if account != nil {
		attrs = append(attrs, "platform", account.Platform, "account_id", account.ID)
	}
	slog.Info("billing.service_tier_downgraded", attrs...)
}
