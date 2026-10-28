package service

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type groupQualityFallbackTestRepo struct {
	schedulerGroupAwareOpenAIAccountRepo
}

func (r groupQualityFallbackTestRepo) ListByGroup(ctx context.Context, groupID int64) ([]Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(ctx, groupID, PlatformOpenAI)
}

type groupQualityFallbackTestGate struct {
	groupTrafficTestGate
	normal   map[int64]bool
	recovery bool
	calls    []GroupQualityFallbackRequest
	groups   []int64
}

func (g *groupQualityFallbackTestGate) RefreshGroupQualityFallback(context.Context, string) (bool, error) {
	return true, nil
}

func (g *groupQualityFallbackTestGate) AcquireGroupQualityTraffic(ctx context.Context, accountID int64, model, lease string) (bool, bool, error) {
	traffic, _ := GroupQualityTrafficRequestFromContext(ctx)
	g.groups = append(g.groups, traffic.GroupID)
	if fallback, ok := GroupQualityFallbackFromContext(ctx); ok {
		g.calls = append(g.calls, fallback)
		allowed := g.recovery && fallback.AccountID == accountID && fallback.GroupID == traffic.GroupID && fallback.Model == model
		return allowed, allowed, nil
	}
	return g.normal[accountID], g.normal[accountID], nil
}

func groupQualityFallbackTestAccount(id, groupID int64, priority int, activeHold bool) Account {
	account := groupQualityRoutingTestAccount(id, groupID, priority, "paused")
	account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
	if activeHold {
		account.Status = "quality_paused"
		account.Extra["quality_protection_reason"] = "automatic cache quality hold"
	} else {
		account.Extra["passive_quality"] = []any{map[string]any{"policy_id": float64(7), "scope": "model", "model": "public-model", "phase": "paused"}}
	}
	return account
}

func groupQualityFallbackService(mode string, accounts []Account, gate *groupQualityFallbackTestGate) *OpenAIGatewayService {
	svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
	svc.accountRepo = groupQualityFallbackTestRepo{schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}
	svc.concurrencyService.passiveQualityGate = gate
	svc.cfg.Gateway.OpenAIWS = newSchedulerTestOpenAIWSV2Config().Gateway.OpenAIWS
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	return svc
}

func TestGroupQualityFallbackRanksAutomaticHoldsAndReplaysContext(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			t.Run(mode+"/"+string(transport), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(43)
				accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 100, false), groupQualityFallbackTestAccount(2, groupID, 0, true), groupQualityFallbackTestAccount(3, groupID, -100, true)}
				accounts[2].Schedulable = false
				gate := &groupQualityFallbackTestGate{recovery: true}
				svc := groupQualityFallbackService(mode, accounts, gate)
				ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
				selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, transport, false)
				require.NoError(t, err)
				require.NotNil(t, selection)
				require.True(t, selection.Acquired)
				require.Nil(t, selection.WaitPlan)
				require.Equal(t, int64(2), selection.Account.ID, "existing score must choose best hard-eligible automatic hold")
				require.Equal(t, "group_quality_fallback", decision.Layer)
				require.Len(t, gate.calls, 1)
				replayed, ok := GroupQualityFallbackFromContext(ContextWithSelectionProfitGate(ctx, selection))
				require.True(t, ok)
				require.Equal(t, gate.calls[0], replayed)
				_, stale := GroupQualityFallbackFromContext(ContextWithSelectionProfitGate(ContextWithSelectionProfitGate(ctx, selection), &AccountSelectionResult{}))
				require.False(t, stale, "next normal selection must clear old emergency authorization")
				require.Equal(t, "quality_paused", accounts[1].Status, "routing must not mutate persistent account state")
				selection.ReleaseFunc()
			})
		}
	}
}

func TestGroupQualityFallbackExitsWhenHealthySupplyReturns(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityRoutingTestAccount(2, groupID, 100, "healthy")}
			gate := &groupQualityFallbackTestGate{recovery: true, normal: map[int64]bool{2: true}}
			svc := groupQualityFallbackService(mode, accounts, gate)
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
			ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: groupID, AccountID: 1, Model: "public-model"})
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID)
			require.Empty(t, gate.calls)
			require.Nil(t, selection.groupQualityFallback)
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityFallbackTreatsInsufficientSamplesAsNormalSupply(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, phase := range []string{"", "insufficient", "watching", "healthy"} {
			for _, admit := range []bool{true, false} {
				t.Run(mode+"/"+phase+"/"+map[bool]string{true: "normal", false: "stale-denial"}[admit], func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					groupID := int64(43)
					accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityRoutingTestAccount(2, groupID, 100, phase)}
					gate := &groupQualityFallbackTestGate{recovery: true, normal: map[int64]bool{2: admit}}
					svc := groupQualityFallbackService(mode, accounts, gate)
					ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
					selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
					if admit {
						require.NoError(t, err)
						require.Equal(t, int64(2), selection.Account.ID)
						require.NotEqual(t, "group_quality_fallback", decision.Layer)
						selection.ReleaseFunc()
					} else {
						require.ErrorIs(t, err, ErrGroupQualityTrafficBudget)
						require.Nil(t, selection)
					}
					require.Empty(t, gate.calls, "insufficient or missing evidence must never trigger emergency selection")
				})
			}
		}
	}
}

func TestGroupQualityFallbackOpenTrialRemainsRecoveryCapacity(t *testing.T) {
	groupID := int64(43)
	account := groupQualityFallbackTestAccount(1, groupID, 0, false)
	account.Extra["passive_quality"] = []any{map[string]any{"policy_id": float64(7), "scope": "model", "model": "public-model", "phase": "trial", "trial_ends_at": time.Now().Add(time.Hour).Format(time.RFC3339)}}
	gate := &groupQualityFallbackTestGate{recovery: true}
	svc := groupQualityFallbackService("legacy", []Account{account}, gate)
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
	require.NoError(t, err)
	require.NotNil(t, selection.groupQualityFallback, "exhausted trial budget still permits fallback when no normal account remains")
	selection.ReleaseFunc()
}

func TestGroupQualityFallbackSpilloverPreservesConversationOwner(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService(mode, []Account{groupQualityFallbackTestAccount(1, groupID, 100, false), groupQualityFallbackTestAccount(2, groupID, 0, true)}, gate)
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:owner": 1}}
			svc.cache = cache
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "owner", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID)
			require.True(t, selection.PreserveStickyBinding)
			require.Equal(t, int64(1), cache.sessionBindings["openai:owner"])
			require.Empty(t, cache.deletedSessions)
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityFallbackStillEnforcesProfitAndExactModel(t *testing.T) {
	groupID := int64(43)
	account := groupQualityFallbackTestAccount(1, groupID, 0, true)
	rate := 0.9
	account.RateMultiplier = &rate
	gate := &groupQualityFallbackTestGate{recovery: true}
	svc := groupQualityFallbackService("legacy", []Account{account}, gate)
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: groupID, platform: PlatformOpenAI, threshold: 0.5, pricingAt: time.Now()})
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
	require.Error(t, err)
	require.Nil(t, selection)
	require.Empty(t, gate.calls)
	markerCtx := WithGroupQualityFallback(context.Background(), GroupQualityFallbackRequest{GroupID: groupID, AccountID: account.ID, Model: "public-model"})
	require.Equal(t, "quality_paused", groupQualityFallbackAccount(markerCtx, &account, "other-model").Status, "marker may only relax its public model")
	markerCtx = WithGroupQualityFallback(context.Background(), GroupQualityFallbackRequest{GroupID: groupID, AccountID: 999, Model: "public-model"})
	require.Equal(t, "quality_paused", groupQualityFallbackAccount(markerCtx, &account, "public-model").Status, "marker may only relax its exact account")
}

func TestGroupQualityFallbackHandlesHoldsBeforeTargetProjectionRefresh(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 100, false), groupQualityFallbackTestAccount(2, groupID, 0, true)}
			for i := range accounts {
				delete(accounts[i].Extra, "group_quality_targets")
			}
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService(mode, accounts, gate)
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID)
			require.NotEmpty(t, gate.calls)
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityFallbackPreviousResponseKeepsExactOwner(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, allow := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "denied", true: "accepted"}[allow], func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(43)
				gate := &groupQualityFallbackTestGate{recovery: allow}
				svc := groupQualityFallbackService(mode, []Account{groupQualityFallbackTestAccount(1, groupID, 100, false), groupQualityFallbackTestAccount(2, groupID, 0, true)}, gate)
				ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
				store := svc.getOpenAIWSStateStore()
				require.NoError(t, store.BindResponseAccount(ctx, groupID, "resp_owner", 1, time.Hour))
				selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "resp_owner", "", "public-model", nil, OpenAIUpstreamTransportResponsesWebsocketV2, false)
				if allow {
					require.NoError(t, err)
					require.Equal(t, int64(1), selection.Account.ID)
					require.True(t, selection.PreserveStickyBinding)
					selection.ReleaseFunc()
				} else {
					require.ErrorIs(t, err, ErrGroupQualityTrafficBudget)
					require.Nil(t, selection)
				}
				require.NotEmpty(t, gate.calls)
				for _, call := range gate.calls {
					require.Equal(t, int64(1), call.AccountID, "incremental state must never migrate to better-ranked peer")
				}
				owner, err := store.GetResponseAccount(ctx, groupID, "resp_owner")
				require.NoError(t, err)
				require.Equal(t, int64(1), owner)
			})
		}
	}
}

func TestGroupQualityFallbackPreservesHardGates(t *testing.T) {
	for _, mutate := range []struct {
		name   string
		change func(*Account)
	}{
		{"manual", func(a *Account) { a.Schedulable = false }},
		{"disabled", func(a *Account) { a.Status = StatusDisabled }},
		{"missing provenance", func(a *Account) { delete(a.Extra, "quality_protection_reason") }},
		{"auth cooldown", func(a *Account) { until := time.Now().Add(time.Hour); a.TempUnschedulableUntil = &until }},
		{"hard rate", func(a *Account) { until := time.Now().Add(time.Hour); a.RateLimitResetAt = &until }},
		{"expired", func(a *Account) {
			until := time.Now().Add(-time.Hour)
			a.ExpiresAt = &until
			a.AutoPauseOnExpired = true
		}},
		{"membership", func(a *Account) { a.GroupIDs = []int64{999} }},
	} {
		t.Run(mutate.name, func(t *testing.T) {
			groupID := int64(43)
			account := groupQualityFallbackTestAccount(1, groupID, 0, true)
			mutate.change(&account)
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService("legacy", []Account{account}, gate)
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: groupID, Model: "public-model"})
			selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
			require.Error(t, err)
			require.Nil(t, selection)
			require.Empty(t, gate.calls)
		})
	}
}

func TestGroupQualityTrafficFollowsAPIKeyFallbackGroup(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, allow := range []bool{false, true} {
			t.Run(mode+"/"+map[bool]string{false: "denied", true: "allowed"}[allow], func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				primary, fallback := int64(43), int64(44)
				key := &APIKey{GroupID: &primary, FallbackGroupID: &fallback, Group: &Group{ID: primary, Name: "primary", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}, FallbackGroup: &Group{ID: fallback, Name: "fallback", Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}}
				gate := &groupQualityFallbackTestGate{normal: map[int64]bool{1: allow}}
				svc := groupQualityFallbackService(mode, []Account{groupQualityRoutingTestAccount(1, fallback, 0, "insufficient")}, gate)
				ctx := WithAPIKeyGroupFallbackRouting(GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: 43, Model: "public-model"}), key)
				selection, _, err := svc.SelectAccountWithScheduler(ctx, key.GroupID, "", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
				if allow {
					require.NoError(t, err)
					require.Equal(t, int64(44), selection.RoutedGroupID)
					selection.ReleaseFunc()
				} else {
					require.ErrorIs(t, err, ErrGroupQualityTrafficBudget)
					require.Nil(t, selection)
				}
				require.NotEmpty(t, gate.groups)
				for _, group := range gate.groups {
					require.Equal(t, int64(44), group)
				}
			})
		}
	}
}
