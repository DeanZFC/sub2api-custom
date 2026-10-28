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
// quality. Emergency fallback uses a request-local copy after durable admission.
// The projection contains only blocked entries; live database admission is the
// final authority if this scheduler snapshot is stale.
func (a *Account) ActivePassRateBlocked(model string) bool {
	if a == nil {
		return false
	}
	holds, _ := a.Extra["active_pass_rate_holds"].(map[string]any)
	return activeTestProjectionBlocks(holds, model)
}

// ActiveTestModelBlocked combines an individual capability verdict with the
// rolling pass-rate gate. It always uses the public request model. A provider alias
// cannot bypass either owner on ordinary routes.
func (a *Account) ActiveTestModelBlocked(model string) bool {
	if a == nil {
		return false
	}
	if a.ActiveTestAllModelsPaused() {
		return true
	}
	holds, _ := a.Extra["active_test_model_holds"].(map[string]any)
	return activeTestProjectionBlocks(holds, model) || a.ActivePassRateBlocked(model)
}

// An all-model failure suspends the account, including models outside the
// monitored selection. Only fresh capability recovery may clear this marker.
func (a *Account) ActiveTestAllModelsPaused() bool {
	if a == nil {
		return false
	}
	paused, _ := a.Extra["active_test_all_models_paused"].(bool)
	return paused
}

func activeTestProjectionBlocks(holds map[string]any, model string) bool {
	model = strings.TrimSpace(model)
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
