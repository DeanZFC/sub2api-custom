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

func TestGroupQualityFallbackCannotRelaxActivePassRateHold(t *testing.T) {
	groupID := int64(43)
	a := groupQualityFallbackTestAccount(1, groupID, 0, true)
	a.Extra["active_pass_rate_holds"] = map[string]any{"public-model": true}
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
	ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: groupID, AccountID: a.ID, Model: "public-model"})
	adjusted := groupQualityFallbackAccount(ctx, &a, "public-model")
	require.Equal(t, StatusActive, adjusted.Status, "existing legacy fallback remains available")
	require.True(t, adjusted.ActivePassRateBlocked("public-model"))
	require.False(t, adjusted.IsSchedulableForModelWithContext(ctx, "public-model"))
	require.True(t, shouldClearStickySessionWithContext(ctx, adjusted, "public-model"))
	require.False(t, groupQualityAutomaticHold(ctx, &a, &groupID, "public-model"))
}
