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

func TestQualityProtectionNeverRelaxesActiveModelHolds(t *testing.T) {
	groupID := int64(43)
	a := qualityHoldTestAccount(1, groupID, 0, true)
	a.Extra["active_test_model_holds"] = map[string]any{"model-a": "failed", "model-b": "failed"}
	a.Extra["active_pass_rate_holds"] = map[string]any{"model-c": "pass rate low"}
	a.Extra["active_test_all_models_paused"] = true
	for _, model := range []string{"model-a", "model-b", "model-c", "unmonitored-model", ""} {
		ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: model})
		require.True(t, groupQualityAutomaticHold(ctx, &a, &groupID, model))
		require.False(t, a.IsSchedulableForModelWithContext(ctx, model))
		require.Equal(t, "quality_paused", a.Status)
	}
	// Only actual recovery projection can restore a model; other holds survive.
	delete(a.Extra, "active_test_all_models_paused")
	holds, ok := a.Extra["active_test_model_holds"].(map[string]any)
	require.True(t, ok)
	delete(holds, "model-a")
	a.Status = StatusActive
	require.False(t, a.ActiveTestModelBlocked("model-a"))
	require.True(t, a.ActiveTestModelBlocked("model-b"))
	require.True(t, a.ActivePassRateBlocked("model-c"))
}
