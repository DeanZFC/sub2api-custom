package handler

import (
	"context"
	"errors"
	"reflect"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
)

// refreshOpenAIWSTurnBillingAPIKey reloads the same auth snapshot as HTTP.
// A long-lived connection is bound to an account and group, so identity or
// connection policy changes require a new connection, never an implicit reroute.
func refreshOpenAIWSTurnBillingAPIKey(ctx context.Context, lookup openAIWSTurnAPIKeyLookup, conn *service.APIKey) (*service.APIKey, error) {
	unavailable := func(err error) (*service.APIKey, error) {
		return nil, service.NewOpenAIWSClientCloseError(coderws.StatusTryAgainLater, "API key authentication is temporarily unavailable", err)
	}
	if lookup == nil || conn == nil || conn.Key == "" {
		return unavailable(nil)
	}
	latest, err := lookup.GetByKey(ctx, conn.Key)
	if err != nil && !errors.Is(err, service.ErrAPIKeyNotFound) {
		return unavailable(err)
	}
	if err != nil || latest == nil || latest.ID != conn.ID || latest.UserID != conn.UserID ||
		latest.User == nil || conn.User == nil || latest.User.ID != conn.User.ID {
		return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "API key is no longer authorized for this connection", err)
	}
	if !reflect.DeepEqual(latest.GroupID, conn.GroupID) || !sameOpenAIWSConnectionPolicy(latest.Group, conn.Group) {
		return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "connection policy changed, please reconnect", nil)
	}
	return latest, nil
}

func sameOpenAIWSConnectionPolicy(a, b *service.Group) bool {
	if a == nil || b == nil {
		return a == b
	}
	// These fields are bound by account selection or payload policy at connect.
	// Model allowlists, status, prices, quotas and concurrency are checked with
	// the fresh snapshot each turn instead of forcing a reconnect on repricing.
	return a.ID == b.ID && a.Platform == b.Platform && a.SubscriptionType == b.SubscriptionType &&
		a.MaxReasoningEffort == b.MaxReasoningEffort && a.MaxReasoningEffortOverLimit == b.MaxReasoningEffortOverLimit &&
		reflect.DeepEqual(a.ReasoningEffortMappings, b.ReasoningEffortMappings) &&
		a.RequireOAuthOnly == b.RequireOAuthOnly && a.RequirePrivacySet == b.RequirePrivacySet &&
		a.ForceOpenAIFast == b.ForceOpenAIFast && a.ForceOpenAIUltrafast == b.ForceOpenAIUltrafast &&
		a.ForceOpenAIServiceTier == b.ForceOpenAIServiceTier && a.AllowImageGeneration == b.AllowImageGeneration
}

type openAIWSTurnSubscriptionLookup interface {
	GetActiveSubscription(context.Context, int64, int64) (*service.UserSubscription, error)
	ValidateAndCheckLimits(*service.UserSubscription, *service.Group) (bool, error)
	EnsureWindowMaintenance(context.Context, *service.UserSubscription) (*service.UserSubscription, error)
}

func checkOpenAIWSTurnSubscription(ctx context.Context, lookup openAIWSTurnSubscriptionLookup, key *service.APIKey, conn *service.UserSubscription) error {
	if key.Group == nil || !key.Group.IsSubscriptionType() {
		return nil
	}
	reject := func(err error) error {
		return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "billing check failed; please reconnect", err)
	}
	if lookup == nil || conn == nil {
		return reject(nil)
	}
	current, err := lookup.GetActiveSubscription(ctx, key.User.ID, key.Group.ID)
	if err != nil || current == nil || current.ID != conn.ID || current.UserID != key.User.ID || current.GroupID != key.Group.ID {
		// Never debit an old subscription after it has been replaced.
		return reject(err)
	}
	maintain, err := lookup.ValidateAndCheckLimits(current, key.Group)
	if maintain {
		current, err = lookup.EnsureWindowMaintenance(ctx, current)
		if err != nil || current == nil {
			return reject(err)
		}
		_, err = lookup.ValidateAndCheckLimits(current, key.Group)
	}
	if err != nil {
		return reject(err)
	}
	return nil
}

// Recheck the authenticated primary binding and, when selected, the effective fallback.
func (h *OpenAIGatewayHandler) authorizeOpenAIWSTurn(ctx context.Context, c *gin.Context, primary, bound *service.APIKey, subscription *service.UserSubscription) (*service.APIKey, error) {
	key, err := refreshOpenAIWSTurnBillingAPIKey(ctx, h.apiKeyService, primary)
	if err != nil {
		return nil, err
	}
	accessDenied := func(err error) error {
		return service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "API key is no longer authorized for this connection", err)
	}
	if err := middleware.ValidateAPIKeyAccess(c, key, h.cfg); err != nil {
		return nil, accessDenied(err)
	}
	if !reflect.DeepEqual(primary.GroupID, bound.GroupID) {
		if bound.GroupID == nil || !reflect.DeepEqual(key.FallbackGroupID, bound.GroupID) ||
			!sameOpenAIWSConnectionPolicy(key.FallbackGroup, bound.Group) {
			return nil, accessDenied(nil)
		}
		// GetByKey returns a request-local key. Apply the same RPM override reset
		// as account selection without mutating the connection's routing state.
		routed := *key
		routed.GroupID, routed.Group = key.FallbackGroupID, key.FallbackGroup
		user := *key.User
		user.UserGroupRPMOverride = nil
		routed.User = &user
		key = &routed
		if err := middleware.ValidateAPIKeyAccess(c, key, h.cfg); err != nil {
			return nil, accessDenied(err)
		}
	}
	if h.cfg == nil || h.cfg.RunMode != config.RunModeSimple {
		if key.Status == service.StatusAPIKeyExpired || key.IsExpired() ||
			key.Status == service.StatusAPIKeyQuotaExhausted || key.IsQuotaExhausted() ||
			((key.Group == nil || !key.Group.IsSubscriptionType()) && key.User.Balance <= 0) {
			return nil, service.NewOpenAIWSClientCloseError(coderws.StatusPolicyViolation, "billing check failed", nil)
		}
		if err := checkOpenAIWSTurnSubscription(ctx, h.wsSubscriptionLookup, key, subscription); err != nil {
			return nil, err
		}
	}
	return key, nil
}
