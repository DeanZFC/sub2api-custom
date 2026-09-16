package service

import "context"

// accountProtectionOutcomeExcludedKey marks requests whose upstream outcome
// must not affect account health/adaptive-concurrency statistics.  Scheduled
// tests, manual connectivity checks and billing probes exercise the same
// account transport as production traffic, but counting them as production
// failures makes a healthy account look degraded (and can lower its runtime
// concurrency).  The marker stays in the request context so retries preserve
// it automatically.
type accountProtectionOutcomeExcludedKey struct{}

// WithAccountProtectionOutcomeExcluded returns a context that opts the request
// out of account-protection outcome accounting.
func WithAccountProtectionOutcomeExcluded(ctx context.Context) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, accountProtectionOutcomeExcludedKey{}, true)
}

// AccountProtectionOutcomeExcluded reports whether outcome accounting should
// be skipped for a request.
func AccountProtectionOutcomeExcluded(ctx context.Context) bool {
	return ctx != nil && ctx.Value(accountProtectionOutcomeExcludedKey{}) == true
}
