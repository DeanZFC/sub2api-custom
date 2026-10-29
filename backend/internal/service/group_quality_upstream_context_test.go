package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestQualityEvidenceRetainsOrdinaryUpstreamCancellationBehavior(t *testing.T) {
	for _, stream := range []bool{false, true} {
		ctx, cancel := context.WithCancel(context.Background())
		ctx = GroupQualityTrafficContext(ctx, GroupQualityTrafficRequest{Key: "turn", GroupID: 8, Model: "public"})
		upstream, release := detachStreamUpstreamContext(ctx, stream)
		defer release()
		cancel()
		if stream {
			require.NoError(t, upstream.Err())
		} else {
			require.ErrorIs(t, upstream.Err(), context.Canceled)
		}
		identity, ok := GroupQualityTrafficRequestFromContext(upstream)
		require.True(t, ok)
		require.Equal(t, "turn", identity.Key)
	}
	ctx, cancel := context.WithCancel(context.Background())
	upstream, release := detachUpstreamContext(ctx)
	defer release()
	cancel()
	require.NoError(t, upstream.Err(), "ordinary detached forwarding keeps billing drain behavior")
}
