package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
)

func publicRequestModel(ctx context.Context) string {
	if ctx == nil {
		return ""
	}
	if public, ok := RequestedPublicModelFromContext(ctx); ok && public != "" {
		return public
	}
	model, _ := ctx.Value(ctxkey.Model).(string)
	return model
}
func requestedRoutingModel(ctx context.Context, requestedModel string) string {
	if model := publicRequestModel(ctx); model != "" {
		return model
	}
	return requestedModel
}
func withRequestedRoutingModel(ctx context.Context, requestedModel string) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if publicRequestModel(ctx) == "" && requestedModel != "" {
		return context.WithValue(ctx, ctxkey.Model, requestedModel)
	}
	return ctx
}
func shouldClearStickySessionWithContext(ctx context.Context, account *Account, requestedModel string) bool {
	if account == nil {
		return false
	}
	if !account.IsSchedulable() || account.ActiveTestModelBlocked(activePassRateRoutingModel(ctx, requestedModel)) {
		return true
	}
	return account.GetRateLimitRemainingTimeWithContext(ctx, requestedModel) > 0
}

// An active intelligence hold preserves continuation ownership until a later test passes.
var ErrAccountQualityHeld = errors.New("account is paused by active intelligence protection")

func activeIntelligenceHold(ctx context.Context, account *Account, groupID *int64, requestedModel string) bool {
	if account == nil || groupID == nil || *groupID <= 0 || !account.IsOpenAI() {
		return false
	}
	return account.Status == "quality_paused" || account.ActiveTestModelBlocked(requestedRoutingModel(ctx, requestedModel))
}
