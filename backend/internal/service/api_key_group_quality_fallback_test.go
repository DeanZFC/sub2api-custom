package service

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func TestAPIKeyGroupFallback_QualityBudgetSelectionErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"success", nil, false},
		{"no accounts", ErrNoAvailableAccounts, true},
		{"no compact accounts", ErrNoAvailableCompactAccounts, true},
		{"quality budget", ErrGroupQualityTrafficBudget, true},
		{"wrapped quality budget", fmt.Errorf("admission: %w", ErrGroupQualityTrafficBudget), true},
		{"live admission rejection", ErrAccountAdmissionDenied, false},
		{"infrastructure failure", errors.New("database unavailable"), false},
		{"cancellation", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"message only", errors.New(ErrGroupQualityTrafficBudget.Error()), false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			primary, fallback := int64(43), int64(44)
			key := &APIKey{
				GroupID: &primary, FallbackGroupID: &fallback,
				Group:         &Group{ID: primary, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
				FallbackGroup: &Group{ID: fallback, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
			}
			ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
			group, fallbackCtx, ok := apiKeyFallbackGroupForSelection(ctx, key.GroupID, tt.err)
			require.Equal(t, tt.want, ok)
			require.Equal(t, int64(43), *key.GroupID, "eligibility alone must not switch billing")
			if !tt.want {
				require.Nil(t, group)
				return
			}
			require.Same(t, key.FallbackGroup, group)
			_, _, retried := apiKeyFallbackGroupForSelection(fallbackCtx, key.GroupID, tt.err)
			require.False(t, retried, "fallback must not recurse")
			forcedCtx := context.WithValue(ctx, ctxkey.ForcePlatform, PlatformOpenAI)
			_, _, forced := apiKeyFallbackGroupForSelection(forcedCtx, key.GroupID, tt.err)
			require.False(t, forced, "forced-platform routes must retain their existing restriction")
			canceledCtx, cancel := context.WithCancel(ctx)
			cancel()
			_, _, canceled := apiKeyFallbackGroupForSelection(canceledCtx, key.GroupID, tt.err)
			require.False(t, canceled, "a stale selection error must not restart a canceled request")
		})
	}
}

func TestAPIKeyGroupFallback_QualityBudgetPreservesResponseOwner(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			for _, canMove := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/%s/can_move=%t", mode, transport, canMove), func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					primary, fallback := int64(43), int64(44)
					key := &APIKey{
						GroupID: &primary, FallbackGroupID: &fallback,
						Group:         &Group{ID: primary, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
						FallbackGroup: &Group{ID: fallback, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
					}
					accounts := []Account{
						groupQualityRoutingTestAccount(1, primary, 0, "insufficient"),
						groupQualityRoutingTestAccount(2, fallback, 0, "insufficient"),
					}
					for i := range accounts {
						accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = true
					}
					gate := &groupQualityFallbackTestGate{normal: map[int64]bool{2: true}}
					svc := groupQualityFallbackService(mode, accounts, gate)
					// The shared scheduler cache fake ignores group IDs. Use the real
					// in-memory store to keep response ownership scoped to its group.
					svc.openaiWSStateStore = NewOpenAIWSStateStore(nil)
					ctx := WithAPIKeyGroupFallbackRouting(GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{
						Key: "continuation", GroupID: primary, Model: "public-model", EstimatedInput: 8192,
					}), key)
					store := svc.getOpenAIWSStateStore()
					require.NoError(t, store.BindResponseAccount(ctx, primary, "resp_primary_owner", 1, time.Hour))
					selection, _, err := svc.SelectAccountWithSchedulerForCapability(ctx, key.GroupID, "resp_primary_owner", "", "public-model", nil, transport, OpenAIEndpointCapabilityResponses, false, canMove, false)
					if canMove {
						require.NoError(t, err)
						require.NotNil(t, selection)
						t.Cleanup(selection.ReleaseFunc)
						require.Equal(t, int64(2), selection.Account.ID)
						require.True(t, selection.UsedAPIKeyFallback)
						require.Equal(t, int64(44), *key.GroupID)
						require.Contains(t, gate.groups, int64(44))
					} else {
						require.ErrorIs(t, err, ErrGroupQualityTrafficBudget)
						require.Nil(t, selection)
						require.Equal(t, int64(43), *key.GroupID)
						require.NotContains(t, gate.groups, int64(44), "incremental state must not move to a different credential")
					}
					owner, ownerErr := store.GetResponseAccount(ctx, 43, "resp_primary_owner")
					require.NoError(t, ownerErr)
					require.Equal(t, int64(1), owner, "original response ownership must remain intact")
				})
			}
		}
	}
}

func TestAPIKeyGroupFallback_PrimaryQualityBudgetExhausted(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			for _, tt := range []struct {
				name             string
				primaryAllowed   bool
				recoveryAllowed  bool
				fallbackAllowed  bool
				withoutFallback  bool
				fallbackDisabled bool
				wantAccount      int64
				wantErr          error
			}{
				{name: "budget exhausted switches group", fallbackAllowed: true, wantAccount: 2},
				{name: "healthy primary wins", primaryAllowed: true, fallbackAllowed: true, wantAccount: 1},
				{name: "primary recovery wins", recoveryAllowed: true, fallbackAllowed: true, wantAccount: 1},
				{name: "both budgets exhausted", wantErr: ErrGroupQualityTrafficBudget},
				{name: "no fallback configured", withoutFallback: true, fallbackAllowed: true, wantErr: ErrGroupQualityTrafficBudget},
				{name: "fallback hard gate retained", fallbackAllowed: true, fallbackDisabled: true, wantErr: ErrNoAvailableAccounts},
			} {
				t.Run(mode+"/"+string(transport)+"/"+tt.name, func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					primary, fallback := int64(43), int64(44)
					key := &APIKey{
						GroupID: &primary, FallbackGroupID: &fallback,
						Group:         &Group{ID: primary, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.2},
						FallbackGroup: &Group{ID: fallback, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true, RateMultiplier: 0.11},
					}
					if tt.withoutFallback {
						key.FallbackGroupID = nil
						key.FallbackGroup = nil
					}
					accounts := []Account{
						groupQualityRoutingTestAccount(1, primary, 0, "insufficient"),
						groupQualityRoutingTestAccount(2, fallback, 0, "insufficient"),
					}
					for i := range accounts {
						accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = true
					}
					if tt.fallbackDisabled {
						accounts[1].Status = StatusDisabled
					}
					gate := &groupQualityFallbackTestGate{
						normal: map[int64]bool{1: tt.primaryAllowed, 2: tt.fallbackAllowed}, recovery: tt.recoveryAllowed,
					}
					svc := groupQualityFallbackService(mode, accounts, gate)
					ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{
						Key: "request", GroupID: primary, Model: "public-model", EstimatedInput: 8192,
					})
					ctx = WithAPIKeyGroupFallbackRouting(ctx, key)
					selection, _, err := svc.SelectAccountWithScheduler(ctx, key.GroupID, "", "", "public-model", nil, transport, false)
					if tt.wantErr != nil {
						require.ErrorIs(t, err, tt.wantErr)
						require.Nil(t, selection)
						require.Equal(t, int64(43), *key.GroupID, "failed fallback must not switch billing")
					} else {
						require.NoError(t, err)
						require.NotNil(t, selection)
						t.Cleanup(selection.ReleaseFunc)
						require.True(t, selection.Acquired)
						require.Equal(t, tt.wantAccount, selection.Account.ID)
						require.Equal(t, tt.wantAccount == 2, selection.UsedAPIKeyFallback)
						if tt.wantAccount == 2 {
							require.Equal(t, int64(44), selection.RoutedGroupID)
							require.Equal(t, int64(44), *key.GroupID)
							require.Same(t, key.FallbackGroup, key.Group)
							require.Equal(t, 0.11, key.Group.RateMultiplier)
							traffic, ok := GroupQualityTrafficRequestFromContext(ctx)
							require.True(t, ok)
							require.Equal(t, int64(44), traffic.GroupID)
							require.Equal(t, "public-model", traffic.Model)
							require.Equal(t, int64(8192), traffic.EstimatedInput)
							require.Nil(t, selection.groupQualityFallback, "primary recovery authorization must not leak")
						} else {
							require.Equal(t, int64(43), *key.GroupID)
							require.NotContains(t, gate.groups, int64(44))
						}
					}
					require.Contains(t, gate.groups, int64(43))
					if tt.wantAccount == 2 || tt.name == "both budgets exhausted" {
						require.Contains(t, gate.groups, int64(44), "fallback must run its own quality admission")
					}
					sawFallback := false
					for _, groupID := range gate.groups {
						if sawFallback {
							require.Equal(t, int64(44), groupID, "must not loop back to the primary group")
						}
						sawFallback = sawFallback || groupID == 44
					}
				})
			}
		}
	}
}
