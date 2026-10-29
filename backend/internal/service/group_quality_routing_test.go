package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

func groupQualityRoutingTestAccount(id, groupID int64, priority int, phase string) Account {
	account := Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: priority, GroupIDs: []int64{groupID}}
	if phase != "" {
		account.Extra = map[string]any{"group_quality_targets": []any{map[string]any{
			"policy_id": float64(1), "group_id": float64(groupID), "model": "public-model", "phase": phase, "verified": phase == "healthy",
			"verified_until": time.Now().Add(time.Hour).Format(time.RFC3339),
		}}}
	}
	return account
}

func groupQualityRoutingTestService(mode string, accounts []Account, cache *schedulerTestGatewayCache, concurrencyCache ConcurrencyCache) *OpenAIGatewayService {
	cfg := &config.Config{}
	cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_nonbatch"
	cfg.Gateway.OpenAIWS.LBTopK = 1
	cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: accounts}, cache: cache, cfg: cfg, concurrencyService: NewConcurrencyService(concurrencyCache)}
	if mode == "advanced" {
		svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true", "true")
	}
	return svc
}

func TestGroupQualityRoutingProjectionIsExactAndConservative(t *testing.T) {
	groupID := int64(43)
	account := groupQualityRoutingTestAccount(1, groupID, 0, "healthy")
	entries, ok := account.Extra["group_quality_targets"].([]any)
	require.True(t, ok)
	entry, ok := entries[0].(map[string]any)
	require.True(t, ok)
	for _, groupValue := range []any{float64(groupID), groupID, int(groupID), json.Number("43")} {
		entry["group_id"] = groupValue
		verified, managed := groupQualityRoutingState(&account, &groupID, "public-model")
		require.True(t, verified)
		require.True(t, managed)
	}
	verified, managed := groupQualityRoutingState(&account, &groupID, "provider-model")
	require.False(t, verified)
	require.False(t, managed)
	otherGroup := groupID + 1
	verified, managed = groupQualityRoutingState(&account, &otherGroup, "public-model")
	require.False(t, verified)
	require.False(t, managed)
	account.Extra["group_quality_targets"] = append(entries, map[string]any{"group_id": groupID, "model": "public-model", "phase": "probing", "verified": false})
	verified, managed = groupQualityRoutingState(&account, &groupID, "public-model")
	require.False(t, verified, "overlapping unverified policy must not claim healthy")
	require.True(t, managed)
}

func TestGroupQualityRoutingProjectionRequiresFreshVerification(t *testing.T) {
	groupID := int64(43)
	for _, until := range []string{"", "malformed", time.Now().Add(-time.Minute).Format(time.RFC3339)} {
		t.Run(until, func(t *testing.T) {
			account := groupQualityRoutingTestAccount(1, groupID, 0, "healthy")
			entries, ok := account.Extra["group_quality_targets"].([]any)
			require.True(t, ok)
			entry, ok := entries[0].(map[string]any)
			require.True(t, ok)
			entry["verified_until"] = until
			verified, managed := groupQualityRoutingState(&account, &groupID, "public-model")
			require.False(t, verified)
			require.True(t, managed, "expired evidence remains visible as managed without implying a hold")
		})
	}
}

func TestGroupQualityRoutingVerifiedStatusDoesNotOverrideCacheAllocation(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(47054)
			verified := groupQualityRoutingTestAccount(1, groupID, 0, "healthy")
			verified.Extra["passive_quality"] = []any{map[string]any{"scope": "model", "model": "public-model", "phase": "cache_limited", "allocation_percent": float64(20)}}
			accounts := []Account{verified, groupQualityRoutingTestAccount(2, groupID, 100, "insufficient")}
			session := ""
			for i := 0; i < 100; i++ {
				candidate := fmt.Sprintf("group-quality-cache-%d", i)
				if !passiveQualityAllowsNewSession(withPassiveQualityAllocationSeed(context.Background(), candidate), &verified, "public-model") {
					session = candidate
					break
				}
			}
			require.NotEmpty(t, session)
			svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
			selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", session, "public-model", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.True(t, selection.Acquired)
			require.Equal(t, int64(2), selection.Account.ID, "verified status must not bypass independent cache allocation protection")
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityHeldOwnerSpillsToWaitWithoutRebinding(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(47055)
			accounts := []Account{groupQualityRoutingTestAccount(1, groupID, 0, "insufficient"), groupQualityRoutingTestAccount(2, groupID, 100, "healthy")}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:existing": 1}}
			cc := groupQualityRoutingDeniedCache{denied: map[int64]bool{1: true}, schedulerTestConcurrencyCache: schedulerTestConcurrencyCache{acquireResults: map[int64]bool{2: false}}}
			svc := groupQualityRoutingTestService(mode, accounts, cache, cc)
			selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "existing", "public-model", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.False(t, selection.Acquired)
			require.NotNil(t, selection.WaitPlan)
			require.Equal(t, int64(2), selection.WaitPlan.AccountID)
			require.True(t, selection.PreserveStickyBinding, "handler must retain owner after delayed admission")
			require.Equal(t, int64(1), cache.sessionBindings["openai:existing"])
			require.Empty(t, cache.deletedSessions)
		})
	}
}

func TestGroupQualityHoldPreviousResponseNeverMigrates(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			ctx := context.Background()
			groupID := int64(47056)
			accounts := []Account{groupQualityRoutingTestAccount(1, groupID, 0, "insufficient"), groupQualityRoutingTestAccount(2, groupID, 100, "healthy")}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:existing": 1}}
			var acquired []int64
			cc := groupQualityRoutingDeniedCache{denied: map[int64]bool{1: true}, schedulerTestConcurrencyCache: schedulerTestConcurrencyCache{acquiredIDs: &acquired}}
			svc := groupQualityRoutingTestService(mode, accounts, cache, cc)
			store := svc.getOpenAIWSStateStore()
			require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_held_owner", 1, time.Hour))
			selection, err := svc.SelectAccountByPreviousResponseID(ctx, &groupID, "resp_held_owner", "public-model", nil, false)
			require.ErrorIs(t, err, ErrAccountQualityHeld)
			require.Nil(t, selection)
			selection, _, err = svc.SelectAccountWithScheduler(ctx, &groupID, "resp_held_owner", "existing", "public-model", nil, OpenAIUpstreamTransportAny, false)
			require.ErrorIs(t, err, ErrAccountQualityHeld)
			require.Nil(t, selection)
			owner, err := store.GetResponseAccount(ctx, groupID, "resp_held_owner")
			require.NoError(t, err)
			require.Equal(t, int64(1), owner)
			require.NotContains(t, acquired, int64(2), "non-replayable incremental input must not reach a fallback")
			require.Equal(t, int64(1), cache.sessionBindings["openai:existing"])
			require.Empty(t, cache.deletedSessions)
		})
	}
}

func TestGroupQualityRoutingAllSchedulersUseAccountPriorityAndRetainFallback(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, scenario := range []string{"insufficient_higher_priority", "insufficient_busy", "insufficient_and_first_healthy_busy", "manual_disabled", "healthy_higher_priority", "other_model", "public_model_mapping"} {
			t.Run(mode+"/"+scenario, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(47051)
				accounts := []Account{
					groupQualityRoutingTestAccount(1, groupID, 100, "healthy"),
					groupQualityRoutingTestAccount(2, groupID, 200, "healthy"),
					groupQualityRoutingTestAccount(3, groupID, 0, "insufficient"),
				}
				wantID := int64(3)
				wantAttempts := []int64{3}
				cc := schedulerTestConcurrencyCache{}
				ctx := context.Background()
				model := "public-model"
				switch scenario {
				case "insufficient_busy":
					cc.acquireResults = map[int64]bool{3: false}
					wantID, wantAttempts = 1, []int64{3, 1}
				case "insufficient_and_first_healthy_busy":
					cc.acquireResults = map[int64]bool{3: false, 1: false}
					wantID, wantAttempts = 2, []int64{3, 1, 2}
				case "manual_disabled":
					accounts[2].Schedulable = false
					wantID, wantAttempts = 1, []int64{1}
				case "healthy_higher_priority":
					accounts[2].Priority = 300
					wantID, wantAttempts = 1, []int64{1}
				case "other_model":
					model, wantID = "another-model", 3
				case "public_model_mapping":
					ctx = context.WithValue(ctx, ctxkey.Model, "public-model")
					model = "provider-model"
				}
				var acquired []int64
				cc.acquiredIDs = &acquired
				svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, cc)
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", model, nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.True(t, selection.Acquired, "try every usable candidate before returning a wait plan")
				require.Equal(t, wantID, selection.Account.ID)
				require.Equal(t, wantAttempts, acquired, "quality verification must not override native account priority or hide peers beyond TopK")
				if selection.ReleaseFunc != nil {
					selection.ReleaseFunc()
				}
			})
		}
	}
}

func TestGroupQualityRoutingEqualPriorityKeepsInsufficientSamplesEligible(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(47058)
			recentlyUsed := time.Now().Add(-time.Minute)
			accounts := []Account{
				groupQualityRoutingTestAccount(2, groupID, 0, "healthy"),
				groupQualityRoutingTestAccount(1, groupID, 0, "insufficient"),
			}
			accounts[0].LastUsedAt = &recentlyUsed
			cc := schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{
				1: {AccountID: 1, LoadRate: 0},
				2: {AccountID: 2, LoadRate: 50},
			}}
			svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, cc)
			svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Load = 1
			selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.True(t, selection.Acquired)
			require.Equal(t, int64(1), selection.Account.ID, "equal-priority insufficient accounts must compete using normal load and recency rules")
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityRoutingPreservesSessionOwnerIncludingWeightedMode(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(47052)
			accounts := []Account{groupQualityRoutingTestAccount(1, groupID, 200, "insufficient"), groupQualityRoutingTestAccount(2, groupID, 0, "healthy")}
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:existing": 1}}
			svc := groupQualityRoutingTestService(mode, accounts, cache, schedulerTestConcurrencyCache{})
			selection, decision, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "existing", "public-model", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(1), selection.Account.ID)
			require.True(t, decision.StickySessionHit)
			require.Equal(t, int64(1), cache.sessionBindings["openai:existing"])
			require.Empty(t, cache.deletedSessions)
			if selection.ReleaseFunc != nil {
				selection.ReleaseFunc()
			}
		})
	}
}

func TestGroupQualityRoutingRetainsCapacityBeyondTopKAfterLiveDenial(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, verifiedPeer := range []bool{true, false} {
			for _, qualityHeld := range []bool{true, false} {
				t.Run(fmt.Sprintf("%s/verified_peer_%t/quality_held_%t", mode, verifiedPeer, qualityHeld), func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					groupID := int64(47057)
					accounts := []Account{groupQualityRoutingTestAccount(1, groupID, 100, "healthy"), groupQualityRoutingTestAccount(2, groupID, 200, "healthy"), groupQualityRoutingTestAccount(3, groupID, 300, "")}
					wantID := int64(2)
					blocked := []int64{1}
					if !verifiedPeer {
						accounts[1].Extra = nil
						blocked = append(blocked, 2)
						wantID = 3
					}
					cc := groupQualityRoutingDeniedCache{denied: map[int64]bool{}, schedulerTestConcurrencyCache: schedulerTestConcurrencyCache{acquireResults: map[int64]bool{}}}
					for _, id := range blocked {
						if qualityHeld {
							cc.denied[id] = true
						} else {
							cc.acquireResults[id] = false
						}
					}
					svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, cc)
					selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportAny, false)
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.True(t, selection.Acquired, "TopK=1 must not hide a usable peer after live rejection")
					require.Equal(t, wantID, selection.Account.ID)
					selection.ReleaseFunc()
				})
			}
		}
	}
}

type groupQualityRoutingDeniedCache struct {
	schedulerTestConcurrencyCache
	denied map[int64]bool
}

func (c groupQualityRoutingDeniedCache) AcquireAccountSlot(ctx context.Context, accountID int64, maxConcurrency int, requestID string) (bool, error) {
	if c.denied[accountID] {
		return false, ErrAccountQualityHeld
	}
	return c.schedulerTestConcurrencyCache.AcquireAccountSlot(ctx, accountID, maxConcurrency, requestID)
}

func TestGroupQualityQualityHoldPreservesOwnerAndTriesOtherAccounts(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, allDenied := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/fallback", true: "/all_denied"}[allDenied], func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(47053)
				accounts := []Account{groupQualityRoutingTestAccount(1, groupID, 0, "insufficient"), groupQualityRoutingTestAccount(2, groupID, 100, "healthy")}
				cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:existing": 1}}
				denied := map[int64]bool{1: true}
				if allDenied {
					denied[2] = true
				}
				svc := groupQualityRoutingTestService(mode, accounts, cache, groupQualityRoutingDeniedCache{denied: denied})
				selection, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", "existing", "public-model", nil, OpenAIUpstreamTransportAny, false)
				if allDenied {
					require.ErrorIs(t, err, ErrAccountQualityHeld)
					require.Nil(t, selection)
				} else {
					require.NoError(t, err)
					require.True(t, selection.Acquired)
					require.True(t, selection.PreserveStickyBinding)
					require.Equal(t, int64(2), selection.Account.ID)
					if selection.ReleaseFunc != nil {
						selection.ReleaseFunc()
					}
				}
				require.Equal(t, int64(1), cache.sessionBindings["openai:existing"])
				require.Empty(t, cache.deletedSessions)
			})
		}
	}
}

func TestGroupQualityRoutingHydrationFailureReleasesSlot(t *testing.T) {
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn", GroupID: 43})
	cache := &openAISnapshotCacheStub{accountsByID: map[int64]*Account{}}
	svc := &OpenAIGatewayService{schedulerSnapshot: NewSchedulerSnapshotService(cache, nil, stubOpenAIAccountRepo{}, nil, nil)}
	released := false
	selection, err := svc.newAcquiredSelectionResult(ctx, &Account{ID: 1001}, func() {
		released = true
	})
	require.Error(t, err)
	require.Nil(t, selection)
	require.True(t, released)
}

func TestGroupQualityRoutingPostAcquireMismatchReleasesSlot(t *testing.T) {
	groupID := int64(43)
	account := groupQualityRoutingTestAccount(1001, groupID, 0, "healthy")
	gate := &qualityAdmissionTestGate{allow: true, reserved: true}
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn", GroupID: groupID, Model: "public-model"})
	var released []int64
	svc := groupQualityRoutingTestService("advanced", []Account{account}, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{releasedIDs: &released})
	svc.concurrencyService.passiveQualityGate = gate
	scheduler := &defaultOpenAIAccountScheduler{service: svc, stats: newOpenAIAccountRuntimeStats()}
	selection, _, err := scheduler.tryAcquireOpenAISelectionOrder(ctx, OpenAIAccountScheduleRequest{
		GroupID: &groupID, Platform: PlatformOpenAI, RequestedModel: "public-model", RequiredTransport: OpenAIUpstreamTransportResponsesWebsocketV2,
	}, []openAIAccountCandidateScore{{account: &account}})
	require.NoError(t, err)
	require.Nil(t, selection, "account does not support required WS transport")
	require.Equal(t, []int64{1001}, released)
	require.Equal(t, []bool{false}, gate.releases)
}
