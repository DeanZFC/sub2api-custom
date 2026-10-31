package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	infraerrors "github.com/Wei-Shaw/sub2api/internal/pkg/errors"
)

type fallbackGroupLoader func(context.Context, int64) (*Group, error)
type accountFallbackBillingGroupKey struct{}

// Deliberately does not unwrap: owner-bound quality errors cannot authorize a cross-group retry.
type ownerBoundQualityError struct{ err error }

func (e *ownerBoundQualityError) Error() string { return e.err.Error() }

func (s *adminServiceImpl) validateUnavailableFallback(ctx context.Context, sourceID int64, platform string, targetID *int64) error {
	if targetID == nil || *targetID <= 0 {
		return nil
	}
	invalid := func(message string) error {
		return infraerrors.New(http.StatusBadRequest, "INVALID_UNAVAILABLE_FALLBACK_GROUP", message)
	}
	if platform == PlatformComposite {
		return invalid("composite groups do not support account fallback")
	}
	seen := map[int64]bool{sourceID: true}
	id := *targetID
	for depth := 0; depth < 32; depth++ {
		if seen[id] {
			return invalid("account fallback groups must not form a cycle")
		}
		seen[id] = true
		group, err := s.groupRepo.GetByIDLite(ctx, id)
		if err != nil || group == nil {
			return invalid("account fallback group does not exist")
		}
		if group.Platform != platform || !group.IsActive() {
			return invalid("account fallback group must be active and use the same platform")
		}
		if group.FallbackGroupIDOnUnavailable == nil || *group.FallbackGroupIDOnUnavailable <= 0 {
			return nil
		}
		id = *group.FallbackGroupIDOnUnavailable
	}
	return invalid("account fallback chain is too long")
}

func unavailableForGroupFallback(ctx context.Context, group *Group, err error) bool {
	if ctx.Err() != nil {
		return false
	}
	if force, _ := ctx.Value(ctxkey.ForcePlatform).(string); force != "" {
		return false
	}
	if group != nil && group.StrictFailover.Enabled && !errors.Is(err, errStrictFailoverAllUnavailable) {
		return false
	}
	return errors.Is(err, ErrNoAvailableAccounts) || errors.Is(err, ErrNoAvailableCompactAccounts) || errors.Is(err, ErrAccountQualityHeld)
}

// Only scheduling uses the fallback group. The authenticated API key and usage
// snapshot retain their billing identity; profit admission uses that same price.
func withAccountFallbackGroup(ctx context.Context, billing, routing *Group) context.Context {
	ctx = context.WithValue(ctx, accountFallbackBillingGroupKey{}, billing)
	ctx = context.WithValue(ctx, ctxkey.Group, routing)
	return context.WithValue(ctx, strictFailoverContextKey{}, (*strictFailoverState)(nil))
}

func accountFallbackBillingGroup(ctx context.Context) *Group {
	group, _ := ctx.Value(accountFallbackBillingGroupKey{}).(*Group)
	return group
}

func (s *OpenAIGatewayService) loadFallbackGroup(ctx context.Context, id int64) (*Group, error) {
	if s.schedulerSnapshot == nil {
		return nil, nil
	}
	return s.schedulerSnapshot.GetGroupByIDLite(ctx, id)
}

// selectWithGroupFallback makes one group-level attempt before API-key fallback.
// It never follows the target's fallback recursively. Every new request starts
// at its own group, and unknown/capacity/infrastructure failures cannot skip a
// strict tier. API-key fallback retains its existing billing switch.
func selectWithGroupFallback[T any](ctx context.Context, groupID *int64, model string, load fallbackGroupLoader,
	selectInGroup func(context.Context, *int64) (T, error), mark func(T, int64, bool)) (T, error) {
	value, err := selectInGroup(ctx, groupID)
	if err == nil || groupID == nil || *groupID <= 0 || ctx.Err() != nil {
		return value, err
	}
	// A forwarding retry after API-key fallback stays in the final pool. Do not
	// expand that pool's own group fallback into a second fallback chain.
	if _, active := apiKeyFallbackRoutingForGroup(ctx, *groupID); active {
		return value, err
	}
	source := strictGroupFromContext(ctx, groupID)
	if source == nil {
		if key, ok := APIKeyForUsageContext(ctx); ok && key.Group != nil && key.Group.ID == *groupID {
			source = key.Group
		}
	}
	if !unavailableForGroupFallback(ctx, source, err) {
		return value, err
	}
	// Internal callers without an authenticated group still resolve the same
	// configuration. Do not add a repository read to successful selections.
	if source == nil && load != nil {
		var loadErr error
		source, loadErr = load(ctx, *groupID)
		if loadErr != nil {
			return value, loadErr
		}
	}
	if !unavailableForGroupFallback(ctx, source, err) {
		return value, err
	}
	primaryErr := err
	if source != nil && source.FallbackGroupIDOnUnavailable != nil && *source.FallbackGroupIDOnUnavailable > 0 && *source.FallbackGroupIDOnUnavailable != source.ID {
		if load == nil {
			return value, fmt.Errorf("group fallback repository is unavailable")
		}
		target, loadErr := load(ctx, *source.FallbackGroupIDOnUnavailable)
		if loadErr != nil {
			return value, loadErr
		}
		if target != nil && target.IsActive() && target.Platform == source.Platform {
			id := target.ID
			fallbackCtx := withAccountFallbackGroup(ctx, source, target)
			value, err = selectInGroup(fallbackCtx, &id)
			if err == nil {
				if mark != nil {
					mark(value, id, false)
				}
				slog.Info("group_unavailable_fallback_selected", "billing_group_id", source.ID, "routed_group_id", id, "model", model)
				return value, nil
			}
			if !unavailableForGroupFallback(fallbackCtx, target, err) {
				return value, err
			}
		}
	}
	fallback, fallbackCtx, ok := apiKeyFallbackGroupForSelection(ctx, groupID, primaryErr)
	if !ok {
		return value, err
	}
	id := fallback.ID
	// API-key fallback charges the target group, so its profit admission must
	// use that target's price too, before the successful selection is committed.
	fallbackCtx = withAccountFallbackGroup(fallbackCtx, fallback, fallback)
	value, err = selectInGroup(fallbackCtx, &id)
	if err == nil {
		activateAPIKeyFallbackRouting(ctx, id)
		if mark != nil {
			mark(value, id, true)
		}
		slog.Info("api_key_group_fallback_selected", "billing_group_id", id, "routed_group_id", id, "model", model)
	}
	return value, err
}

func markGroupFallbackSelection(selection *AccountSelectionResult, id int64, apiKeyFallback bool) {
	if selection != nil && selection.Account != nil {
		selection.RoutedGroupID = id
		selection.UsedAPIKeyFallback = apiKeyFallback
		selection.UsedGroupFallback = !apiKeyFallback
	}
}
