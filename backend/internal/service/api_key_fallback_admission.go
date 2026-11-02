package service

import "context"

type fallbackBillingAdmissionKey struct{}
type admittedFallbackSubscriptionKey struct{}
type admittedFallbackSubscription struct {
	groupID      int64
	subscription *UserSubscription
}

// Copy only the admitted billing identity into detached usage work; callbacks
// and mutable routing/concurrency state never cross that boundary.
func CopyAPIKeyFallbackSubscription(source, target context.Context) context.Context {
	if snapshot, ok := source.Value(admittedFallbackSubscriptionKey{}).(admittedFallbackSubscription); ok {
		return context.WithValue(target, admittedFallbackSubscriptionKey{}, snapshot)
	}
	if r := fallbackRouting(source); r != nil && r.billingAdmitted && isAPIKeyFallbackActive(r.apiKey) {
		snapshot := admittedFallbackSubscription{groupID: r.fallbackGroup.ID}
		if r.subscription != nil {
			sub := *r.subscription
			snapshot.subscription = &sub
		}
		return context.WithValue(target, admittedFallbackSubscriptionKey{}, snapshot)
	}
	return target
}

type APIKeyFallbackAdmissionError struct{ Cause error }

func (e *APIKeyFallbackAdmissionError) Error() string {
	return "fallback group admission: " + e.Cause.Error()
}
func (e *APIKeyFallbackAdmissionError) Unwrap() error { return e.Cause }

func fallbackRouting(ctx context.Context) *apiKeyGroupFallbackRouting {
	if ctx == nil {
		return nil
	}
	r, _ := ctx.Value(apiKeyGroupFallbackContextKey{}).(*apiKeyGroupFallbackRouting)
	return r
}

// The existing admission phases install request-local callbacks. Selection
// invokes them once for the effective billing group before any upstream work.
func admitAPIKeyFallback(ctx context.Context) error {
	r := fallbackRouting(ctx)
	if r == nil || isAPIKeyFallbackActive(r.apiKey) {
		return nil
	}
	candidate := *r.apiKey
	id := r.fallbackGroup.ID
	candidate.GroupID, candidate.Group = &id, r.fallbackGroup
	if candidate.User != nil {
		user := *candidate.User
		user.UserGroupRPMOverride = nil
		candidate.User = &user
	}
	var sub *UserSubscription
	var err error
	if r.billingAdmission != nil {
		sub, err = r.billingAdmission(context.WithValue(ctx, fallbackBillingAdmissionKey{}, true), &candidate)
		if err != nil {
			return &APIKeyFallbackAdmissionError{err}
		}
	}
	if r.concurrencyAdmission != nil {
		if err = r.concurrencyAdmission(ctx, candidate.Group); err != nil {
			return &APIKeyFallbackAdmissionError{err}
		}
	}
	if r.reservationAdmission != nil {
		if err = r.reservationAdmission(ctx, &candidate, sub); err != nil {
			return &APIKeyFallbackAdmissionError{err}
		}
	}
	r.subscription, r.billingAdmitted = sub, r.billingAdmission != nil
	return nil
}

func RegisterAPIKeyFallbackReservation(ctx context.Context, fn func(context.Context, *APIKey, *UserSubscription) error) {
	if r := fallbackRouting(ctx); r != nil {
		r.reservationAdmission = fn
	}
}

func SetAPIKeyFallbackReservation(ctx context.Context, reservation *InflightReservation) {
	if r := fallbackRouting(ctx); r != nil {
		r.reservation = reservation
	}
}
