package service

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestRetiredPassiveProtectionCannotRestrictAccount(t *testing.T) {
	account := activeRoutingTestAccount(1, 43, 7)
	account.ProxyIDs = []int64{11, 22}
	account.Extra["passive_quality"] = []any{
		map[string]any{"scope": "account", "phase": "paused"},
		map[string]any{"scope": "model", "model": "public-model", "phase": "limited", "concurrency_limit": float64(1), "priority_penalty": float64(100)},
	}
	require.True(t, account.IsSchedulable(), "legacy metadata cannot retain a removed passive hold")
	require.Equal(t, 16, account.TotalConcurrency())
	require.False(t, shouldClearStickySessionWithContext(context.Background(), &account, "public-model"))
	require.False(t, activeIntelligenceHold(context.Background(), &account, &account.GroupIDs[0], "public-model"))
	require.Equal(t, 7, account.Priority)
}

func TestActiveIntelligenceHoldRemainsModelScoped(t *testing.T) {
	account := activeRoutingTestAccount(1, 43, 0)
	account.Extra["active_test_model_holds"] = map[string]any{"public-model": true}
	ctx := withRequestedRoutingModel(context.Background(), "public-model")
	require.Equal(t, "public-model", requestedRoutingModel(ctx, "provider-alias"))
	require.True(t, shouldClearStickySessionWithContext(ctx, &account, "provider-alias"))
	require.True(t, activeIntelligenceHold(ctx, &account, &account.GroupIDs[0], "provider-alias"))
	require.False(t, shouldClearStickySessionWithContext(context.Background(), &account, "another-model"))
	account.Status = StatusDisabled
	require.True(t, shouldClearStickySessionWithContext(context.Background(), &account, "another-model"))
}
