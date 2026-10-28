package service

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestActivePassRateRoutingScopesPublicModelAndUnknownRequest(t *testing.T) {
	a := &Account{ID: 1, Platform: PlatformOpenAI, Status: StatusActive, Schedulable: true, Extra: map[string]any{
		"active_pass_rate_holds": map[string]any{"public-a": "pass rate is below threshold"},
	}}
	require.True(t, a.ActivePassRateBlocked("public-a"))
	require.True(t, a.ActivePassRateBlocked(""), "missing model must not bypass a capability hold")
	require.False(t, a.ActivePassRateBlocked("public-b"))
	require.False(t, a.IsSchedulableForModel("public-a"))
	require.True(t, a.IsSchedulableForModel("public-b"))
	require.True(t, shouldClearStickySessionWithContext(context.Background(), a, "public-a"))
	require.False(t, shouldClearStickySessionWithContext(context.Background(), a, "public-b"))
	ctx := context.WithValue(context.Background(), ctxkey.Model, "provider-a")
	ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, "public-a")
	require.Equal(t, "public-a", ActivePassRateRequestModel(ctx))
	require.False(t, a.IsSchedulableForModelWithContext(ctx, "provider-a"))
	require.True(t, shouldClearStickySessionWithContext(ctx, a, "provider-a"))
	traffic := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: 8, Model: "public-a"})
	traffic = context.WithValue(traffic, ctxkey.Model, "provider-a")
	require.Equal(t, "public-a", ActivePassRateRequestModel(traffic))
	require.False(t, a.IsSchedulableForModelWithContext(traffic, "provider-a"))
	require.Equal(t, "", ActivePassRateRequestModel(nil)) //nolint:staticcheck // Intentionally exercise the nil-context defensive branch.
}

func TestGroupQualityFallbackTemporarilyRelaxesActivePassRateHold(t *testing.T) {
	groupID := int64(43)
	a := groupQualityFallbackTestAccount(1, groupID, 0, true)
	a.Extra["active_pass_rate_holds"] = map[string]any{"public-model": true, "other-model": true}
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
	ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: groupID, AccountID: a.ID, Model: "public-model"})
	adjusted := groupQualityFallbackAccount(ctx, &a, "public-model")
	require.Equal(t, StatusActive, adjusted.Status, "existing legacy fallback remains available")
	require.False(t, adjusted.ActivePassRateBlocked("public-model"))
	require.True(t, a.ActivePassRateBlocked("public-model"), "original holds remain")
	require.True(t, adjusted.ActivePassRateBlocked("other-model"))
	require.True(t, adjusted.IsSchedulableForModelWithContext(ctx, "public-model"))
	require.False(t, shouldClearStickySessionWithContext(ctx, adjusted, "public-model"))
	require.True(t, groupQualityAutomaticHold(ctx, &a, &groupID, "public-model"))
}

func TestActiveTestModelHoldAcrossProvidersAndRecovery(t *testing.T) {
	for _, platform := range []string{PlatformOpenAI, PlatformAnthropic, PlatformGemini, PlatformAntigravity} {
		t.Run(platform, func(t *testing.T) {
			a := &Account{ID: 1, Platform: platform, Status: StatusActive, Schedulable: true, Extra: map[string]any{
				"active_test_model_holds": map[string]any{"public-a": "capability test failed"},
				"active_pass_rate_holds":  map[string]any{"public-b": "pass rate low"},
			}}
			require.False(t, a.IsSchedulableForModel("public-a"))
			require.False(t, a.IsSchedulableForModel("public-b"))
			require.True(t, a.IsSchedulableForModel("public-c"))
			require.True(t, a.ActiveTestModelBlocked(""), "unknown model must not bypass a hold")
			ctx := context.WithValue(context.Background(), ctxkey.RequestedPublicModel, "public-a")
			require.False(t, a.IsSchedulableForModelWithContext(ctx, "provider-alias"))
			require.True(t, shouldClearStickySessionWithContext(ctx, a, "provider-alias"))
			holds, ok := a.Extra["active_test_model_holds"].(map[string]any)
			require.True(t, ok)
			delete(holds, "public-a")
			require.True(t, a.IsSchedulableForModel("public-a"), "fresh recovery releases only the recovered model")
			require.False(t, a.IsSchedulableForModel("public-b"))
		})
	}
}

func TestGroupQualityFallbackTemporarilyRelaxesActiveTestModelHold(t *testing.T) {
	groupID := int64(43)
	a := groupQualityFallbackTestAccount(1, groupID, 0, true)
	a.Extra["active_test_model_holds"] = map[string]any{"public-model": "pelican review failed", "other-model": "failed"}
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
	ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: groupID, AccountID: a.ID, Model: "public-model"})
	adjusted := groupQualityFallbackAccount(ctx, &a, "public-model")
	require.False(t, adjusted.ActiveTestModelBlocked("public-model"))
	require.True(t, a.ActiveTestModelBlocked("public-model"), "original holds remain")
	require.True(t, adjusted.ActiveTestModelBlocked("other-model"))
	require.True(t, adjusted.IsSchedulableForModelWithContext(ctx, "provider-alias"))
	require.False(t, shouldClearStickySessionWithContext(ctx, adjusted, "provider-alias"))
	require.True(t, groupQualityAutomaticHold(ctx, &a, &groupID, "public-model"))
}

func TestAllMonitoredModelsFailedFallbackIsRequestScoped(t *testing.T) {
	groupID := int64(43)
	a := groupQualityFallbackTestAccount(1, groupID, 0, true)
	a.Extra["active_test_model_holds"] = map[string]any{"model-a": "failed", "model-b": "failed"}
	a.Extra["active_test_all_models_paused"] = true
	for _, model := range []string{"model-a", "model-b", "unmonitored-model", ""} {
		require.True(t, a.ActiveTestModelBlocked(model))
		require.False(t, a.IsSchedulableForModel(model))
		ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: model})
		ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: groupID, AccountID: a.ID, Model: model})
		adjusted := groupQualityFallbackAccount(ctx, &a, model)
		if model == "" {
			require.Same(t, &a, adjusted, "missing model never authorizes fallback")
		} else {
			require.Equal(t, StatusActive, adjusted.Status)
			require.True(t, adjusted.IsSchedulableForModelWithContext(ctx, model))
			require.True(t, groupQualityAutomaticHold(ctx, &a, &groupID, model))
		}
		require.True(t, a.ActiveTestModelBlocked(model), "fallback never clears durable holds")
	}
	// A fresh model recovery removes the aggregate pause, while the still-bad
	// monitored model remains blocked by its individual projection.
	delete(a.Extra, "active_test_all_models_paused")
	holds, ok := a.Extra["active_test_model_holds"].(map[string]any)
	require.True(t, ok)
	delete(holds, "model-a")
	a.Status = StatusActive
	require.False(t, a.ActiveTestModelBlocked("model-a"))
	require.True(t, a.ActiveTestModelBlocked("model-b"))
	require.False(t, a.ActiveTestModelBlocked("unmonitored-model"))
}
