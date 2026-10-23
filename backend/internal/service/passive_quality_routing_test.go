package service

import (
	"context"
	"fmt"
	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/pkg/openai_compat"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func cacheLimitedAccount(id int64, percent int) *Account {
	return &Account{ID: id, Extra: map[string]any{"passive_quality": []any{map[string]any{"scope": "model", "model": "m", "phase": "cache_limited", "allocation_percent": float64(percent)}}}}
}

func TestPassiveCacheNewSessionSamplingPreservesExistingAndRecoveryTraffic(t *testing.T) {
	limited := cacheLimitedAccount(1, 20)
	healthy := &Account{ID: 2}
	admitted := 0
	rejected := 0
	for i := 0; i < 1000; i++ {
		ctx := withPassiveQualityAllocationSeed(context.Background(), fmt.Sprintf("session-%d", i))
		first := passiveQualityAllowsNewSession(ctx, limited, "m")
		require.Equal(t, first, passiveQualityAllowsNewSession(ctx, limited, "m"))
		if first {
			admitted++
		} else {
			rejected++
			require.Equal(t, []*Account{healthy}, passiveQualityNewSessionCandidates(ctx, []*Account{limited, healthy}, "m"))
			require.Equal(t, []*Account{limited, healthy}, passiveQualityNewSessionCandidates(ctx, []*Account{limited, healthy}, "m", limited.ID))
			require.Equal(t, []*Account{limited}, passiveQualityNewSessionCandidates(ctx, []*Account{limited}, "m"))
		}
	}
	require.Greater(t, admitted, 120)
	require.Less(t, admitted, 280)
	require.Greater(t, rejected, 700)
	require.Equal(t, 100, limited.PassiveQualityNewSessionPercent("another-model"))
	require.False(t, limited.PassiveQualityBlocked("m"))
}

func TestPassiveCacheAllocationCombinesOverlappingHoldsAndPublicModel(t *testing.T) {
	a := cacheLimitedAccount(1, 40)
	states, ok := a.Extra["passive_quality"].([]any)
	require.True(t, ok)
	a.Extra["passive_quality"] = append(states, map[string]any{"scope": "account", "phase": "cache_limited", "allocation_percent": float64(10)}, map[string]any{"scope": "account", "phase": "limited", "allocation_percent": float64(1)})
	require.Equal(t, 10, a.PassiveQualityNewSessionPercent("m"))
	require.Equal(t, 10, a.PassiveQualityNewSessionPercent("another"))
	states, ok = a.Extra["passive_quality"].([]any)
	require.True(t, ok)
	a.Extra["passive_quality"] = append(states, map[string]any{"scope": "account", "phase": "cache_limited", "allocation_percent": float64(0)})
	require.Equal(t, 10, a.PassiveQualityNewSessionPercent("m"))
}

func TestPassiveCacheLegacyAndWeightedSchedulersRetainOwnerAndReduceNewPlacement(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(fmt.Sprint(advanced), func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(47001)
			limited := cacheLimitedAccount(1, 20)
			limited.Platform = PlatformOpenAI
			limited.Type = AccountTypeAPIKey
			limited.Status = StatusActive
			limited.Schedulable = true
			limited.Concurrency = 8
			limited.Priority = 0
			limited.GroupIDs = []int64{groupID}
			healthy := Account{ID: 2, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 100, GroupIDs: []int64{groupID}}
			cfg := &config.Config{}
			cfg.Gateway.OpenAIWS.LBTopK = 1
			cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
			cache := &schedulerTestGatewayCache{}
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{*limited, healthy}}, cache: cache, cfg: cfg, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
			if advanced {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true", "true")
			}
			session := ""
			for i := 0; i < 100; i++ {
				candidate := fmt.Sprintf("sample-%d", i)
				if !passiveQualityAllowsNewSession(withPassiveQualityAllocationSeed(context.Background(), candidate), limited, "m") {
					session = candidate
					break
				}
			}
			require.NotEmpty(t, session)
			selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", session, "m", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(2), selected.Account.ID)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			// Changing allocation must preserve an existing owner even when weighted
			// scoring's top-K would otherwise choose a higher-priority peer.
			cache.sessionBindings["openai:"+session] = 1
			repo, ok := svc.accountRepo.(schedulerTestOpenAIAccountRepo)
			require.True(t, ok)
			repo.accounts[0].Priority = 200
			repo.accounts[1].Priority = 0
			svc.accountRepo = repo
			selected, decision, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", session, "m", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, int64(1), selected.Account.ID)
			require.True(t, decision.StickySessionHit)
			if selected.ReleaseFunc != nil {
				selected.ReleaseFunc()
			}
			require.Empty(t, cache.deletedSessions)
		})
	}
}

func TestPassiveCacheSchedulersUseSampledOutFallbackAfterLiveFailure(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		advanced := mode == "advanced"
		for _, failure := range []string{"busy", "fresh_rate_limit", "both_hard_blocked"} {
			t.Run(fmt.Sprintf("%s/%s", mode, failure), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				groupID := int64(47002)
				primary := Account{ID: 41, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 100, GroupIDs: []int64{groupID}}
				fallback := *cacheLimitedAccount(42, 20)
				fallback.Platform, fallback.Type, fallback.Status = PlatformOpenAI, AccountTypeAPIKey, StatusActive
				fallback.Schedulable, fallback.Concurrency, fallback.Priority, fallback.GroupIDs = true, 8, 0, []int64{groupID}
				freshPrimary, freshFallback := primary, fallback
				if failure != "busy" {
					until := time.Now().Add(time.Hour)
					freshPrimary.RateLimitResetAt = &until
					if failure == "both_hard_blocked" {
						freshFallback.RateLimitResetAt = &until
					}
				}
				snapshot := &openAISnapshotCacheStub{snapshotAccounts: []*Account{&primary, &fallback}, accountsByID: map[int64]*Account{primary.ID: &freshPrimary, fallback.ID: &freshFallback}}
				acquiredIDs := []int64{}
				cc := schedulerTestConcurrencyCache{acquiredIDs: &acquiredIDs}
				if failure == "busy" {
					cc.acquireResults = map[int64]bool{primary.ID: false, fallback.ID: true}
				}
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "legacy_nonbatch"
				cfg.Gateway.OpenAIWS.LBTopK = 1
				cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{freshPrimary, freshFallback}}, cache: &schedulerTestGatewayCache{}, cfg: cfg, schedulerSnapshot: &SchedulerSnapshotService{cache: snapshot}, concurrencyService: NewConcurrencyService(cc)}
				if advanced {
					svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true", "true")
				}
				session := ""
				for i := 0; i < 100; i++ {
					candidate := fmt.Sprintf("fallback-%d", i)
					if !passiveQualityAllowsNewSession(withPassiveQualityAllocationSeed(context.Background(), candidate), &fallback, "m") {
						session = candidate
						break
					}
				}
				require.NotEmpty(t, session)
				selected, _, err := svc.SelectAccountWithScheduler(context.Background(), &groupID, "", session, "m", nil, OpenAIUpstreamTransportAny, false)
				if failure == "both_hard_blocked" {
					require.Error(t, err)
					require.Nil(t, selected)
					return
				}
				require.NoError(t, err)
				require.NotNil(t, selected)
				require.Equal(t, fallback.ID, selected.Account.ID)
				require.True(t, selected.Acquired, "available fallback must be acquired before returning a wait plan")
				require.Nil(t, selected.WaitPlan)
				require.Contains(t, acquiredIDs, fallback.ID)
				if selected.ReleaseFunc != nil {
					selected.ReleaseFunc()
				}
			})
		}
	}
}

func TestPassiveCacheGatewayFallsBackAfterPreferredSlotBusy(t *testing.T) {
	for _, mode := range []string{"batch", "load_error", "nonbatch"} {
		t.Run(mode, func(t *testing.T) {
			primary := Account{ID: 51, Platform: PlatformAnthropic, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 100}
			fallback := *cacheLimitedAccount(52, 20)
			fallback.Platform, fallback.Type, fallback.Status = PlatformAnthropic, AccountTypeAPIKey, StatusActive
			fallback.Schedulable, fallback.Concurrency, fallback.Priority = true, 8, 0
			acquired := []int64{}
			cc := schedulerTestConcurrencyCache{acquireResults: map[int64]bool{primary.ID: false, fallback.ID: true}, acquiredIDs: &acquired}
			if mode == "load_error" {
				cc.loadBatchErr = fmt.Errorf("load cache unavailable")
			}
			cfg := &config.Config{}
			cfg.Gateway.Scheduling.LoadBatchEnabled = mode != "nonbatch"
			svc := &GatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{primary, fallback}}, cfg: cfg, concurrencyService: NewConcurrencyService(cc)}
			session := ""
			for i := 0; i < 100; i++ {
				candidate := fmt.Sprintf("generic-fallback-%d", i)
				if !passiveQualityAllowsNewSession(withPassiveQualityAllocationSeed(context.Background(), candidate), &fallback, "m") {
					session = candidate
					break
				}
			}
			require.NotEmpty(t, session)
			ctx := context.WithValue(context.Background(), ctxkey.ForcePlatform, PlatformAnthropic)
			selected, err := svc.SelectAccountWithLoadAwareness(ctx, nil, session, "m", nil, "", 0)
			require.NoError(t, err)
			require.NotNil(t, selected)
			require.True(t, selected.Acquired)
			require.Equal(t, fallback.ID, selected.Account.ID)
			require.Equal(t, []int64{primary.ID, fallback.ID}, acquired)
			selected.ReleaseFunc()
		})
	}
}

// The live gate records both calls separately so a correct routing snapshot
// cannot hide a mismatched model passed to concurrency or trial admission.
type passiveModelRoutingGate struct {
	PassiveQualityRepository
	capModel, admissionModel string
	limit                    int
}

func (g *passiveModelRoutingGate) PassiveQualityConcurrencyLimit(_ context.Context, _ int64, model string) (int, error) {
	g.capModel = model
	if model == "public-a" {
		return g.limit, nil
	}
	return 0, nil
}
func (g *passiveModelRoutingGate) AcquirePassiveQuality(_ context.Context, _ int64, model, _ string) (bool, bool, error) {
	g.admissionModel = model
	return true, false, nil
}

type passiveModelRoutingCache struct {
	schedulerTestConcurrencyCache
	limits map[int64]int
}

func (c *passiveModelRoutingCache) AcquireAccountSlot(ctx context.Context, id int64, limit int, request string) (bool, error) {
	c.limits[id] = limit
	return c.schedulerTestConcurrencyCache.AcquireAccountSlot(ctx, id, limit, request)
}

func TestPassiveQualityRoutingUsesPublicModelAcrossMapping(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_batch", "advanced", "generic", "generic_batch"} {
		for _, source := range []string{"request", "composite"} {
			for _, public := range []string{"public-a", "public-b"} {
				for _, holdModel := range []string{"public-a", "provider-model"} {
					t.Run(fmt.Sprintf("%s/%s/%s/hold=%s", mode, source, public, holdModel), func(t *testing.T) {
						resetOpenAIAdvancedSchedulerSettingCacheForTest()
						platform := PlatformOpenAI
						generic := mode == "generic" || mode == "generic_batch"
						if generic {
							platform = PlatformAnthropic
						}
						primary := Account{ID: 801, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 0, Extra: map[string]any{"passive_quality": []any{map[string]any{"scope": "model", "model": holdModel, "phase": "paused"}}}}
						backup := Account{ID: 802, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 100}
						cfg := &config.Config{}
						cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "legacy_batch" || mode == "generic_batch"
						cfg.Gateway.OpenAIWS.LBTopK = 1
						cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
						gate := &passiveModelRoutingGate{}
						concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{})
						concurrency.passiveQualityGate = gate
						ctx := context.WithValue(context.Background(), ctxkey.Model, public)
						if source == "composite" {
							ctx = context.WithValue(context.WithValue(context.Background(), ctxkey.Model, "provider-model"), ctxkey.RequestedPublicModel, public)
						}
						var selected *AccountSelectionResult
						var err error
						if generic {
							ctx = context.WithValue(ctx, ctxkey.ForcePlatform, platform)
							svc := &GatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{primary, backup}}, cfg: cfg, concurrencyService: concurrency}
							selected, err = svc.SelectAccountWithLoadAwareness(ctx, nil, "", "provider-model", nil, "", 0)
						} else {
							svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{primary, backup}}, cfg: cfg, concurrencyService: concurrency}
							if mode == "advanced" {
								svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
							}
							selected, _, err = svc.SelectAccountWithScheduler(ctx, nil, "", "", "provider-model", nil, OpenAIUpstreamTransportAny, false)
						}
						require.NoError(t, err)
						want := primary.ID
						if public == holdModel {
							want = backup.ID
						}
						require.Equal(t, want, selected.Account.ID)
						require.Equal(t, public, gate.capModel)
						require.Equal(t, public, gate.admissionModel)
						if selected.ReleaseFunc != nil {
							selected.ReleaseFunc()
						}
						require.Equal(t, 8, primary.Concurrency)
						require.Equal(t, 0, primary.Priority)
					})
				}
			}
		}
	}
}

func TestPassiveQualityRoutingPenaltyAndConcurrencyRemainModelScoped(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_batch", "advanced", "generic", "generic_batch"} {
		for _, public := range []string{"public-a", "public-b"} {
			t.Run(mode+"/"+public, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				generic := mode == "generic" || mode == "generic_batch"
				platform := PlatformOpenAI
				if generic {
					platform = PlatformAnthropic
				}
				account := Account{ID: 811, Platform: platform, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 3, Extra: map[string]any{"passive_quality": []any{map[string]any{"scope": "model", "model": "public-a", "phase": "limited", "concurrency_limit": float64(2), "priority_penalty": float64(5)}}}}
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = mode == "legacy_batch" || mode == "generic_batch"
				cache := &passiveModelRoutingCache{limits: map[int64]int{}}
				gate := &passiveModelRoutingGate{limit: 2}
				concurrency := NewConcurrencyService(cache)
				concurrency.passiveQualityGate = gate
				ctx := context.WithValue(context.Background(), ctxkey.Model, public)
				var selected *AccountSelectionResult
				var err error
				if generic {
					ctx = context.WithValue(ctx, ctxkey.ForcePlatform, platform)
					svc := &GatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cfg: cfg, concurrencyService: concurrency}
					selected, err = svc.SelectAccountWithLoadAwareness(ctx, nil, "", "provider-model", nil, "", 0)
				} else {
					svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{account}}, cfg: cfg, concurrencyService: concurrency}
					if mode == "advanced" {
						svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
					}
					selected, _, err = svc.SelectAccountWithScheduler(ctx, nil, "", "", "provider-model", nil, OpenAIUpstreamTransportAny, false)
				}
				require.NoError(t, err)
				wantLimit, wantPriority := 8, 3
				if public == "public-a" {
					wantLimit, wantPriority = 2, 8
				}
				require.Equal(t, wantLimit, cache.limits[account.ID])
				require.Equal(t, wantPriority, selected.Account.Priority)
				require.Equal(t, public, gate.admissionModel)
				selected.ReleaseFunc()
				require.Equal(t, 8, account.Concurrency)
				require.Equal(t, 3, account.Priority)
			})
		}
	}
}

func TestPassiveQualityPublicIdentityDoesNotBypassProviderCooldown(t *testing.T) {
	until := time.Now().Add(time.Hour).Format(time.RFC3339)
	account := Account{ID: 821, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{modelRateLimitsKey: map[string]any{"provider-model": map[string]any{"rate_limit_reset_at": until}}}}
	ctx := context.WithValue(context.Background(), ctxkey.Model, "public-a")
	require.False(t, account.IsSchedulableForModelWithContext(ctx, "provider-model"))
	require.True(t, shouldClearStickySessionWithContext(ctx, &account, "provider-model"))
	require.True(t, account.IsSchedulableForModelWithContext(ctx, "different-provider-model"))
}

func TestPassiveQualityStickyClearUsesPublicIdentity(t *testing.T) {
	account := Account{ID: 822, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Extra: map[string]any{"passive_quality": []any{map[string]any{"scope": "model", "model": "public-a", "phase": "paused"}}}}
	ctxA := context.WithValue(context.Background(), ctxkey.Model, "public-a")
	ctxB := context.WithValue(context.Background(), ctxkey.Model, "public-b")
	require.True(t, shouldClearStickySessionWithContext(ctxA, &account, "provider-model"))
	require.False(t, shouldClearStickySessionWithContext(ctxB, &account, "provider-model"))
	account.Status = StatusError
	require.True(t, shouldClearStickySessionWithContext(ctxB, &account, "provider-model"))
}

func TestPassiveQualityActualStickyRoutingKeepsOtherPublicModel(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		for _, public := range []string{"public-a", "public-b"} {
			t.Run(fmt.Sprintf("advanced=%v/%s", advanced, public), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				group := int64(48001)
				primary := Account{ID: 831, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 100, GroupIDs: []int64{group}, Extra: map[string]any{"passive_quality": []any{map[string]any{"scope": "model", "model": "public-a", "phase": "paused"}}}}
				backup := Account{ID: 832, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: 0, GroupIDs: []int64{group}}
				cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:sticky-public": primary.ID}}
				cfg := &config.Config{}
				cfg.Gateway.Scheduling.LoadBatchEnabled = true
				svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{primary, backup}}, cache: cache, cfg: cfg, concurrencyService: NewConcurrencyService(schedulerTestConcurrencyCache{})}
				if advanced {
					svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
				}
				ctx := context.WithValue(context.Background(), ctxkey.Model, public)
				selected, decision, err := svc.SelectAccountWithScheduler(ctx, &group, "", "sticky-public", "provider-model", nil, OpenAIUpstreamTransportAny, false)
				require.NoError(t, err)
				if public == "public-b" {
					require.Equal(t, primary.ID, selected.Account.ID)
					require.True(t, decision.StickySessionHit)
					require.Empty(t, cache.deletedSessions)
				} else {
					require.Equal(t, backup.ID, selected.Account.ID)
					require.False(t, decision.StickySessionHit)
				}
				selected.ReleaseFunc()
			})
		}
	}
}

func TestPassiveQualityRoutingSeedsMissingAdmissionModel(t *testing.T) {
	for _, advanced := range []bool{false, true} {
		t.Run(fmt.Sprint(advanced), func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			a := Account{ID: 841, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8}
			gate := &passiveModelRoutingGate{}
			concurrency := NewConcurrencyService(schedulerTestConcurrencyCache{})
			concurrency.passiveQualityGate = gate
			svc := &OpenAIGatewayService{accountRepo: schedulerTestOpenAIAccountRepo{accounts: []Account{a}}, cfg: &config.Config{}, concurrencyService: concurrency}
			if advanced {
				svc.rateLimitService = newOpenAIAdvancedSchedulerRateLimitService("true")
			}
			selected, _, err := svc.SelectAccountWithScheduler(context.Background(), nil, "", "", "requested-a", nil, OpenAIUpstreamTransportAny, false)
			require.NoError(t, err)
			require.Equal(t, "requested-a", gate.capModel)
			require.Equal(t, "requested-a", gate.admissionModel)
			selected.ReleaseFunc()
		})
	}
}

func TestPassiveQualityModelScopedProbeMapsPublicModelWithoutClearingOtherHold(t *testing.T) {
	holds := []any{map[string]any{"scope": "model", "model": "public-a", "phase": "probing"}, map[string]any{"scope": "model", "model": "public-b", "phase": "paused"}}
	account := &Account{ID: 851, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 1,
		Credentials: map[string]any{"api_key": "test", "base_url": "https://upstream.example.com", "model_mapping": map[string]any{"public-a": "provider-a", "public-b": "provider-b"}},
		Extra:       map[string]any{openai_compat.ExtraKeyResponsesSupported: false, "passive_quality": holds}}
	upstream := &modelIdentityHTTPUpstream{body: "{\"choices\":[{\"message\":{\"content\":\"OK\"},\"finish_reason\":\"stop\"}]}"}
	tester := &AccountTestService{accountRepo: &modelIdentityAccountRepo{account: account}, httpUpstream: upstream, cfg: &config.Config{}}
	repo := &passiveProbeTestRepo{}
	service := NewPassiveQualityService(repo, tester)
	defer service.cancel()
	state := PassiveQualityState{AccountID: account.ID, PolicyID: 1, Version: 2, Generation: 4, Model: "public-a", ProbeModel: "public-b"}
	service.probe(state)
	require.Equal(t, "provider-a", upstream.requestModel)
	require.True(t, repo.success)
	require.Equal(t, state, repo.probe)
	require.Equal(t, holds, account.Extra["passive_quality"])
	require.True(t, account.PassiveQualityBlocked("public-b"))
	require.Equal(t, StatusActive, account.Status)
}
