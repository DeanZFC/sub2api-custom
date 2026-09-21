package service

import (
	"math"
	"time"

	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

const UpstreamBillingRateLimitExtraKey = "upstream_billing_rate_limit"

var ErrUpstreamBillingRateLimitRequiresProbe = infraerrors.BadRequest(
	"UPSTREAM_BILLING_RATE_LIMIT_REQUIRES_PROBE",
	"automatic upstream billing detection cannot be disabled while an upstream rate limit is configured",
)

// UpstreamBillingRateLimit returns the administrator's optional ceiling. Zero
// is a valid ceiling for accounts that should only use a free upstream rate.
func (a *Account) UpstreamBillingRateLimit() (float64, bool) {
	if !isUpstreamBillingProbeAccount(a) {
		return 0, false
	}
	return upstreamBillingRateLimitValue(a.Extra)
}

func upstreamBillingRateLimitValue(extra map[string]any) (float64, bool) {
	// JSON strings and booleans must not silently become configuration values.
	if _, isString := extra[UpstreamBillingRateLimitExtraKey].(string); isString {
		return 0, false
	}
	value, ok := resolveAccountExtraNumber(extra, UpstreamBillingRateLimitExtraKey)
	return value, ok && value >= 0 && !math.IsNaN(value) && !math.IsInf(value, 0)
}

// IsUpstreamBillingRateLimited is derived from the latest successful billing
// data, independent of the manual schedulable/status fields. Failed probes
// retain this data so a network failure cannot unlock an expensive account.
func (a *Account) IsUpstreamBillingRateLimited() bool {
	return a.IsUpstreamBillingRateLimitedAt(time.Now())
}

func (a *Account) IsUpstreamBillingRateLimitedAt(now time.Time) bool {
	limit, configured := a.UpstreamBillingRateLimit()
	if !configured {
		return false
	}
	snapshot := decodeUpstreamBillingProbeSnapshot(a.Extra)
	if snapshot == nil {
		return false
	}
	rate, known := upstreamBillingRateAt(snapshot.Data, now)
	if !known || rate <= limit {
		return false
	}
	// A peak multiplication can round a mathematically equal decimal one ULP
	// above the configured ceiling (for example 0.1 * 3 versus 0.3). Allow only
	// that rounding step; zero remains exact so every positive price is blocked.
	return limit == 0 || rate > math.Nextafter(limit, math.Inf(1))
}

// normalizeUpstreamBillingRateLimitExtra validates only explicit edits. Missing
// keys remain missing (bulk updates merge them); null explicitly removes the limit.
func normalizeUpstreamBillingRateLimitExtra(platform, accountType string, extra map[string]any) error {
	raw, provided := extra[UpstreamBillingRateLimitExtraKey]
	if !provided || raw == nil {
		return nil
	}
	if !IsUpstreamBillingProbeIdentity(platform, accountType) {
		return ErrUpstreamBillingProbeAccountInvalid
	}
	value, valid := upstreamBillingRateLimitValue(extra)
	if !valid {
		return infraerrors.BadRequest("INVALID_UPSTREAM_BILLING_RATE_LIMIT", "upstream_billing_rate_limit must be a finite number greater than or equal to zero, or null")
	}
	extra[UpstreamBillingRateLimitExtraKey] = value
	return nil
}
