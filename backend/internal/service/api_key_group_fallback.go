package service

import (
	"context"
	"errors"
	"log/slog"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

type apiKeyGroupFallbackContextKey struct{}
type apiKeyGroupFallbackAttemptContextKey struct{}

type apiKeyGroupFallbackRouting struct {
	primaryGroupID int64
	fallbackGroup  *Group
}

// WithAPIKeyGroupFallbackRouting installs API-key-level fallback routing.
// Billing, quotas, RPM and concurrency remain attached to the primary API-key group.
func WithAPIKeyGroupFallbackRouting(ctx context.Context, apiKey *APIKey) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if apiKey == nil || apiKey.GroupID == nil || apiKey.FallbackGroupID == nil ||
		apiKey.Group == nil || apiKey.FallbackGroup == nil ||
		*apiKey.GroupID <= 0 || *apiKey.FallbackGroupID <= 0 ||
		*apiKey.GroupID == *apiKey.FallbackGroupID ||
		apiKey.Group.Platform != apiKey.FallbackGroup.Platform ||
		!apiKey.FallbackGroup.IsActive() || !IsGroupContextValid(apiKey.FallbackGroup) {
		return ctx
	}
	return context.WithValue(ctx, apiKeyGroupFallbackContextKey{}, apiKeyGroupFallbackRouting{
		primaryGroupID: *apiKey.GroupID,
		fallbackGroup:  apiKey.FallbackGroup,
	})
}

func apiKeyFallbackGroupForSelection(ctx context.Context, groupID *int64, selectionErr error) (*Group, context.Context, bool) {
	if ctx == nil || groupID == nil || *groupID <= 0 {
		return nil, ctx, false
	}
	if !errors.Is(selectionErr, ErrNoAvailableAccounts) && !errors.Is(selectionErr, ErrNoAvailableCompactAccounts) {
		return nil, ctx, false
	}
	if attempted, _ := ctx.Value(apiKeyGroupFallbackAttemptContextKey{}).(bool); attempted {
		return nil, ctx, false
	}
	if forcePlatform, _ := ctx.Value(ctxkey.ForcePlatform).(string); forcePlatform != "" {
		return nil, ctx, false
	}
	routing, ok := ctx.Value(apiKeyGroupFallbackContextKey{}).(apiKeyGroupFallbackRouting)
	if !ok || routing.primaryGroupID != *groupID || routing.fallbackGroup == nil || !routing.fallbackGroup.IsActive() {
		return nil, ctx, false
	}
	fallbackCtx := context.WithValue(ctx, apiKeyGroupFallbackAttemptContextKey{}, true)
	return routing.fallbackGroup, fallbackCtx, true
}

func markAPIKeyFallbackSelection(selection *AccountSelectionResult, fallbackGroupID int64) *AccountSelectionResult {
	if selection != nil && selection.Account != nil {
		selection.RoutedGroupID = fallbackGroupID
		selection.UsedAPIKeyFallback = true
	}
	return selection
}

func selectAccountWithAPIKeyGroupFallback(
	ctx context.Context,
	groupID *int64,
	requestedModel string,
	selectInGroup func(context.Context, *int64) (*Account, error),
) (*Account, error) {
	account, err := selectInGroup(ctx, groupID)
	fallbackGroup, fallbackCtx, ok := apiKeyFallbackGroupForSelection(ctx, groupID, err)
	if !ok {
		return account, err
	}

	fallbackGroupID := fallbackGroup.ID
	slog.Info("api_key_group_fallback_attempt",
		"primary_group_id", derefGroupID(groupID),
		"fallback_group_id", fallbackGroupID,
		"model", requestedModel)
	account, err = selectInGroup(fallbackCtx, &fallbackGroupID)
	if err == nil && account != nil {
		slog.Info("api_key_group_fallback_selected",
			"primary_group_id", derefGroupID(groupID),
			"fallback_group_id", fallbackGroupID,
			"account_id", account.ID,
			"model", requestedModel)
	}
	return account, err
}
