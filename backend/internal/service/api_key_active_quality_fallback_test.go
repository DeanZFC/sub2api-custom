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

func qualityAdmissionTestAccount(id, groupID int64) Account {
	account := activeRoutingTestAccount(id, groupID, 0)
	account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	return account
}

func TestAPIKeyGroupFallback_QualityHoldSelectionErrors(t *testing.T) {
	for _, tt := range []struct {
		name string
		err  error
		want bool
	}{
		{"success", nil, false},
		{"no accounts", ErrNoAvailableAccounts, true},
		{"no compact accounts", ErrNoAvailableCompactAccounts, true},
		{"quality hold", ErrAccountQualityHeld, true},
		{"wrapped quality hold", fmt.Errorf("admission: %w", ErrAccountQualityHeld), true},
		{"live admission rejection", ErrAccountAdmissionDenied, false},
		{"infrastructure failure", errors.New("database unavailable"), false},
		{"cancellation", context.Canceled, false},
		{"deadline", context.DeadlineExceeded, false},
		{"message only", errors.New(ErrAccountQualityHeld.Error()), false},
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

func TestAPIKeyGroupFallback_QualityHoldPreservesResponseOwner(t *testing.T) {
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
						qualityAdmissionTestAccount(1, primary),
						qualityAdmissionTestAccount(2, fallback),
					}
					gate := &activeHoldTestGate{normal: map[int64]bool{2: true}}
					svc := activeHoldService(mode, accounts, gate)
					// The shared scheduler cache fake ignores group IDs. Use the real
					// in-memory store to keep response ownership scoped to its group.
					svc.openaiWSStateStore = NewOpenAIWSStateStore(nil)
					ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
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
						require.ErrorIs(t, err, ErrAccountQualityHeld)
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

func TestAPIKeyGroupFallback_PrimaryQualityHoldRejected(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			for _, tt := range []struct {
				name             string
				primaryAllowed   bool
				fallbackAllowed  bool
				withoutFallback  bool
				fallbackDisabled bool
				wantAccount      int64
				wantErr          error
			}{
				{name: "quality held switches group", fallbackAllowed: true, wantAccount: 2},
				{name: "unprotected primary wins", primaryAllowed: true, fallbackAllowed: true, wantAccount: 1},
				{name: "both accounts held", wantErr: ErrAccountQualityHeld},
				{name: "no fallback configured", withoutFallback: true, fallbackAllowed: true, wantErr: ErrAccountQualityHeld},
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
						qualityAdmissionTestAccount(1, primary),
						qualityAdmissionTestAccount(2, fallback),
					}
					if tt.fallbackDisabled {
						accounts[1].Status = StatusDisabled
					}
					gate := &activeHoldTestGate{
						normal: map[int64]bool{1: tt.primaryAllowed, 2: tt.fallbackAllowed},
					}
					svc := activeHoldService(mode, accounts, gate)
					ctx := context.Background()
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
						} else {
							require.Equal(t, int64(43), *key.GroupID)
							require.NotContains(t, gate.groups, int64(44))
						}
					}
					require.Contains(t, gate.groups, int64(43))
					if tt.wantAccount == 2 || tt.name == "both accounts held" {
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

// A hold can be committed after snapshot selection but before final account
// admission. The final gate must preserve the same routing/ownership behavior.
func TestAPIKeyGroupFallback_FinalGateQualityHold(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			for _, continuation := range []string{"new", "owner_bound", "replayable"} {
				t.Run(mode+"/"+string(transport)+"/"+continuation, func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					primary, backup := int64(43), int64(44)
					key := &APIKey{GroupID: &primary, FallbackGroupID: &backup,
						Group:         &Group{ID: primary, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true},
						FallbackGroup: &Group{ID: backup, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}}
					accounts := []Account{qualityAdmissionTestAccount(1, primary), qualityAdmissionTestAccount(2, backup)}
					active := &activeHoldTestGate{normal: map[int64]bool{1: true, 2: true}}
					gateway := activeHoldService(mode, accounts, active)
					gateway.openaiWSStateStore = NewOpenAIWSStateStore(nil)
					var checked []int64
					gateway.concurrencyService.SetAccountAdmissionGate(accountAdmissionGateFunc(func(_ context.Context, id int64) (bool, error) {
						checked = append(checked, id)
						if id == 1 {
							return false, ErrAccountQualityHeld
						}
						return true, nil
					}))
					ctx := WithAPIKeyGroupFallbackRouting(context.Background(), key)
					previous := ""
					if continuation != "new" {
						previous = "resp_race_owner"
						require.NoError(t, gateway.getOpenAIWSStateStore().BindResponseAccount(ctx, primary, previous, 1, time.Hour))
					}
					selection, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &primary, previous, "", "public-model", nil, transport, OpenAIEndpointCapabilityResponses, false, continuation == "replayable", false)
					if continuation == "owner_bound" {
						require.ErrorIs(t, err, ErrAccountQualityHeld)
						require.Nil(t, selection)
						require.NotContains(t, checked, int64(2))
						require.EqualValues(t, 43, *key.GroupID)
					} else {
						require.NoError(t, err)
						require.NotNil(t, selection)
						require.True(t, selection.Acquired)
						require.EqualValues(t, 2, selection.Account.ID)
						require.True(t, selection.UsedAPIKeyFallback)
						selection.ReleaseFunc()
					}
					require.Contains(t, checked, int64(1), "test must reach the final gate after successful snapshot selection")
					if previous != "" {
						owner, err := gateway.getOpenAIWSStateStore().GetResponseAccount(ctx, 43, previous)
						require.NoError(t, err)
						require.EqualValues(t, 1, owner)
					}
				})
			}
		}
	}
}
