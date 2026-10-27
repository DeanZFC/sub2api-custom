package service

import (
	"context"
	"strings"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

// ActivePassRateRequestModel keeps capability admission keyed by the public
// request model even when a provider alias or a fallback route is in use.
func ActivePassRateRequestModel(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if public, ok := RequestedPublicModelFromContext(ctx); ok {
		return public
	}
	if traffic, ok := GroupQualityTrafficRequestFromContext(ctx); ok && strings.TrimSpace(traffic.Model) != "" {
		return strings.TrimSpace(traffic.Model)
	}
	model, _ := ctx.Value(ctxkey.Model).(string)
	return strings.TrimSpace(model)
}

func activePassRateRoutingModel(ctx context.Context, requestedModel string) string {
	if model := ActivePassRateRequestModel(ctx); model != "" {
		return model
	}
	return strings.TrimSpace(requestedModel)
}

// ActivePassRateBlocked is independent of legacy active protection and passive
// quality. Fallback may relax those owners, but may never relax capability.
// The projection contains only blocked entries; live database admission is the
// final authority if this scheduler snapshot is stale.
func (a *Account) ActivePassRateBlocked(model string) bool {
	if a == nil {
		return false
	}
	model = strings.TrimSpace(model)
	holds, _ := a.Extra["active_pass_rate_holds"].(map[string]any)
	for heldModel, value := range holds {
		if blocked, ok := value.(bool); ok && !blocked {
			continue
		}
		if entry, ok := value.(map[string]any); ok {
			if blocked, ok := entry["blocked"].(bool); ok && !blocked {
				continue
			}
		}
		if model == "" || heldModel == "" || heldModel == model {
			return true
		}
	}
	return false
}
