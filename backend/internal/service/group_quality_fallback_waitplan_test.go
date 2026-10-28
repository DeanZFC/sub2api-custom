package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestGroupQualityFallbackUsesImmediateCapacityBeforeNormalWaitPlan(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, entry := range []string{"scheduler", "load awareness"} {
			for _, scenario := range []string{
				"normal full recovery free", "both full", "healthy free", "manual disabled", "auth error", "cooldown", "other group", "excluded", "live hard denial", "recovery admission denied",
			} {
				t.Run(mode+"/"+entry+"/"+scenario, func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					groupID := int64(43)
					normal := groupQualityRoutingTestAccount(1, groupID, 0, "healthy")
					recovery := groupQualityFallbackTestAccount(2, groupID, 100, true)
					gate := &requestSupplyFallbackTestGate{
						groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true, normal: map[int64]bool{1: true}},
						live:                         []*Account{&normal},
					}
					loads := map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: normal.Concurrency, LoadRate: 100}}
					acquire := map[int64]bool{1: false}
					var excluded map[int64]struct{}
					switch scenario {
					case "both full":
						loads[2] = &AccountLoadInfo{AccountID: 2, CurrentConcurrency: recovery.Concurrency, LoadRate: 100}
						acquire[2] = false
					case "healthy free":
						loads[1] = &AccountLoadInfo{AccountID: 1}
						acquire[1] = true
					case "manual disabled":
						recovery.Schedulable = false
					case "auth error":
						recovery.Status = StatusError
					case "cooldown":
						until := time.Now().Add(time.Hour)
						recovery.TempUnschedulableUntil = &until
					case "other group":
						recovery.GroupIDs = []int64{44}
					case "excluded":
						excluded = map[int64]struct{}{2: {}}
					case "recovery admission denied":
						gate.recovery = false
					}
					svc := groupQualityFallbackService(mode, []Account{normal, recovery}, &gate.groupQualityFallbackTestGate)
					svc.concurrencyService.passiveQualityGate = gate
					svc.concurrencyService.cache = schedulerTestConcurrencyCache{loadMap: loads, acquireResults: acquire}
					svc.cfg.Gateway.Scheduling.FallbackWaitTimeout = 19 * time.Second
					svc.cfg.Gateway.Scheduling.FallbackMaxWaiting = 37
					if scenario == "live hard denial" {
						svc.concurrencyService.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) { return false, nil }))
					}
					ctx, _ := groupQualityAffinityRequest(&groupID, "public-model", "")
					var selection *AccountSelectionResult
					var err error
					var decision OpenAIAccountScheduleDecision
					if entry == "scheduler" {
						selection, decision, err = svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", excluded, OpenAIUpstreamTransportHTTPSSE, false)
					} else {
						selection, err = svc.SelectAccountWithLoadAwareness(ctx, &groupID, "", "public-model", excluded)
					}
					require.NoError(t, err)
					require.NotNil(t, selection)
					switch scenario {
					case "normal full recovery free":
						require.True(t, selection.Acquired)
						require.Nil(t, selection.WaitPlan)
						require.Equal(t, int64(2), selection.Account.ID)
						require.NotNil(t, selection.groupQualityFallback)
						require.Equal(t, 1, gate.checks, "live request supply is checked before fallback admission")
						if entry == "scheduler" {
							require.Equal(t, "group_quality_fallback", decision.Layer)
						}
						selection.ReleaseFunc()
					case "healthy free":
						require.True(t, selection.Acquired)
						require.Equal(t, int64(1), selection.Account.ID)
						require.Nil(t, selection.groupQualityFallback)
						require.Empty(t, gate.calls)
						selection.ReleaseFunc()
					default:
						require.False(t, selection.Acquired)
						require.NotNil(t, selection.WaitPlan)
						require.Equal(t, int64(1), selection.Account.ID)
						require.Equal(t, int64(1), selection.WaitPlan.AccountID)
						require.Equal(t, 19*time.Second, selection.WaitPlan.Timeout)
						require.Equal(t, 37, selection.WaitPlan.MaxWaiting)
						require.Nil(t, selection.groupQualityFallback, "failed fallback preserves the normal plan")
					}
				})
			}
		}
	}
}

func TestGroupQualityFallbackWaitPlanKeepsGuardianParent(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, stickyEscape := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "guardian wait", true: "after sticky escape"}[stickyEscape], func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(43)
				parentID := "22222222-2222-4222-8222-222222222222"
				parentHash := DeriveSessionHashFromSeed(parentID)
				parent := groupQualityRoutingTestAccount(1, groupID, 0, "healthy")
				recovery := groupQualityFallbackTestAccount(2, groupID, 100, true)
				gate := &groupQualityFallbackTestGate{recovery: true, normal: map[int64]bool{1: true}}
				svc := groupQualityFallbackService(mode, []Account{parent, recovery}, gate)
				svc.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:" + parentHash: 1}}
				svc.concurrencyService.cache = schedulerTestConcurrencyCache{
					loadMap:        map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: parent.Concurrency, LoadRate: 100}},
					acquireResults: map[int64]bool{1: false},
				}
				svc.cfg.Gateway.Scheduling.StickySessionMaxWaiting = 3
				svc.cfg.Gateway.Scheduling.StickySessionWaitTimeout = 23 * time.Second
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = stickyEscape
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
				ctx := guardianAffinityTestContext(t, codexAutoReviewModel, "guardian", parentID, "")
				ctx = GroupQualityTrafficContext(ctx, GroupQualityTrafficRequest{Key: "guardian-request", GroupID: groupID, Model: codexAutoReviewModel})
				selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "guardian-child", codexAutoReviewModel, nil, OpenAIUpstreamTransportHTTPSSE, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.False(t, selection.Acquired)
				require.Equal(t, int64(1), selection.WaitPlan.AccountID)
				if !stickyEscape {
					require.Equal(t, openAIAccountScheduleLayerGuardianParent, decision.Layer)
				}
				require.Empty(t, gate.calls, "quality fallback cannot break the guardian parent binding")
			})
		}
	}
}

func TestGroupQualityFallbackWaitPlanKeepsPreviousResponseOwner(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			owner := groupQualityRoutingTestAccount(1, groupID, 0, "healthy")
			owner.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			recovery := groupQualityFallbackTestAccount(2, groupID, 100, true)
			gate := &groupQualityFallbackTestGate{recovery: true, normal: map[int64]bool{1: true}}
			svc := groupQualityFallbackService(mode, []Account{owner, recovery}, gate)
			svc.concurrencyService.cache = schedulerTestConcurrencyCache{
				loadMap:        map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: owner.Concurrency, LoadRate: 100}},
				acquireResults: map[int64]bool{1: false},
			}
			ctx, _ := groupQualityAffinityRequest(&groupID, "public-model", "")
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, "resp_busy_owner", owner.ID, time.Hour))
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "resp_busy_owner", "", "public-model", nil, OpenAIUpstreamTransportResponsesWebsocketV2, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.False(t, selection.Acquired)
			require.NotNil(t, selection.WaitPlan)
			require.Equal(t, owner.ID, selection.WaitPlan.AccountID)
			require.Empty(t, gate.calls, "incremental response state cannot move to a free recovery peer")
		})
	}
}
