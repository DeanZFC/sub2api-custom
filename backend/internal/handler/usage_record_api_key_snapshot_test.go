//go:build unit

package handler

import (
	"context"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestWrapUsageRecordTaskContextCapturesAPIKeyBeforeFallbackMutation(t *testing.T) {
	primaryID := int64(71001)
	fallbackID := int64(71002)
	originalPrimaryID := primaryID
	apiKey := &service.APIKey{
		GroupID:         &primaryID,
		FallbackGroupID: &fallbackID,
		Group:           &service.Group{ID: primaryID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true},
		FallbackGroup:   &service.Group{ID: fallbackID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true},
	}
	parent := service.WithAPIKeyGroupFallbackRouting(context.Background(), apiKey)

	var captured *service.APIKey
	task := wrapUsageRecordTaskContext(parent, func(ctx context.Context) {
		captured, _ = service.APIKeyUsageSnapshotFromContext(ctx)
	})

	// Simulate a later turn switching the mutable request API key after the
	// usage task has already been queued.
	*apiKey.GroupID = fallbackID
	apiKey.Group = apiKey.FallbackGroup
	task(context.Background())

	require.NotNil(t, captured)
	require.NotNil(t, captured.GroupID)
	require.Equal(t, originalPrimaryID, *captured.GroupID)
	require.NotNil(t, captured.Group)
	require.Equal(t, originalPrimaryID, captured.Group.ID)
}
