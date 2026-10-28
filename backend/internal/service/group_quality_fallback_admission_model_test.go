package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGroupQualityFallbackAdmissionModelCurrentTurnOverride(t *testing.T) {
	const initialPublic, initialForward = "public-initial", "forward-initial"
	base := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn-2", GroupID: 43, Model: initialPublic})
	marker := GroupQualityFallbackRequest{GroupID: 43, AccountID: 1, Model: initialPublic, supply: &groupQualityFallbackSupplyScope{requestedModel: initialForward, publicModel: initialPublic}}
	base = WithGroupQualityFallback(base, marker)
	require.Equal(t, initialForward, GroupQualityFallbackAdmissionModel(base, initialPublic))

	for _, public := range []string{initialPublic, "public-next"} {
		t.Run(public, func(t *testing.T) {
			ctx := WithGroupQualityFallbackAdmissionModel(base, " forward-current ")
			current := marker
			current.Model = public
			ctx = WithGroupQualityFallback(ctx, current)
			require.Equal(t, "forward-current", GroupQualityFallbackAdmissionModel(ctx, public))
			replayed, ok := GroupQualityFallbackFromContext(ctx)
			require.True(t, ok)
			require.Equal(t, public, replayed.Model, "mapping must not change public protection identity")
			traffic, ok := GroupQualityTrafficRequestFromContext(ctx)
			require.True(t, ok)
			require.Equal(t, initialPublic, traffic.Model, "admission override must not mutate traffic identity")
			require.Equal(t, "forward-current", GroupQualityFallbackAdmissionModel(WithoutGroupQualityFallback(ctx), public), "ordinary WS retry keeps the same turn's forwarded model")
		})
	}
	require.Equal(t, initialForward, GroupQualityFallbackAdmissionModel(base, initialPublic), "a later turn must not mutate the earlier scope")
	require.Equal(t, "forward-only", GroupQualityFallbackAdmissionModel(WithGroupQualityFallbackAdmissionModel(nil, "forward-only"), "public")) //nolint:staticcheck // Deliberately exercise the helper's nil-context contract (SA1012).
	require.Equal(t, "public", GroupQualityFallbackAdmissionModel(nil, "public"))                                                               //nolint:staticcheck // Deliberately exercise the helper's nil-context contract (SA1012).
}
