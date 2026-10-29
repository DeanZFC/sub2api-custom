package service

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type userAffinityTestCache struct {
	schedulerTestGatewayCache
	mu       sync.Mutex
	bindings map[string]int64
}

func (c *userAffinityTestCache) GetSessionAccountID(_ context.Context, group int64, key string) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if id := c.bindings[fmt.Sprintf("%d:%s", group, key)]; id > 0 {
		return id, nil
	}
	return 0, ErrStickySessionNotFound
}
func (c *userAffinityTestCache) SetSessionAccountID(_ context.Context, group int64, key string, id int64, _ time.Duration) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bindings == nil {
		c.bindings = map[string]int64{}
	}
	c.bindings[fmt.Sprintf("%d:%s", group, key)] = id
	return nil
}
func (c *userAffinityTestCache) CompareAndSetSessionAccountID(_ context.Context, group int64, key string, expected, id int64, _ time.Duration) (bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.bindings == nil {
		c.bindings = map[string]int64{}
	}
	k := fmt.Sprintf("%d:%s", group, key)
	if c.bindings[k] != expected {
		return false, nil
	}
	c.bindings[k] = id
	return true, nil
}

func TestOpenAIUserAffinityRequiresAuthenticatedIdentityAndScopesPublicModel(t *testing.T) {
	cache := &userAffinityTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	group := int64(43)
	ctx, session := svc.withOpenAIUserAffinity(context.Background(), &group, "client-supplied-user-7", "public-model")
	require.Nil(t, openAIUserAffinityFromContext(ctx))
	require.Equal(t, "client-supplied-user-7", session)
	authenticated := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
	ctx, _ = svc.withOpenAIUserAffinity(authenticated, &group, "session-a", "public-model")
	require.NotNil(t, openAIUserAffinityFromContext(ctx))
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &group, "session-a", 1))
	for _, test := range []struct {
		name        string
		user, group int64
		model       string
		want        int64
	}{
		{"same user across keys", 7, 43, "public-model", 1},
		{"another user", 8, 43, "public-model", 0},
		{"another group", 7, 44, "public-model", 0},
		{"another public model", 7, 43, "other-model", 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			c := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 202, UserID: test.user})
			c, _ = svc.withOpenAIUserAffinity(c, &test.group, "session-b", test.model)
			require.Equal(t, test.want, openAIUserAffinityFromContext(c).userAccountID)
		})
	}
	scoped := context.WithValue(authenticated, ctxkey.UserID, int64(9))
	scoped = context.WithValue(scoped, ctxkey.Model, "public-alias")
	scoped, _ = svc.withOpenAIUserAffinity(scoped, &group, "session", "provider-model")
	require.Equal(t, openAIUserAffinityKey(9, group, "public-alias"), openAIUserAffinityFromContext(scoped).key)
	zeroGroup := int64(0)
	ctx, _ = svc.withOpenAIUserAffinity(authenticated, &zeroGroup, "session", "public-model")
	require.Nil(t, openAIUserAffinityFromContext(ctx), "unresolved groups must not share a global affinity scope")
}

func TestOpenAIUserAffinityConcurrentFirstBindingAndStaleRequest(t *testing.T) {
	for _, sameSession := range []bool{true, false} {
		t.Run(fmt.Sprint(sameSession), func(t *testing.T) {
			cache := &userAffinityTestCache{}
			svc := &OpenAIGatewayService{cache: cache}
			group := int64(43)
			auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
			session1, session2 := "first", "second"
			if sameSession {
				session2 = session1
			}
			ctx1, _ := svc.withOpenAIUserAffinity(auth, &group, session1, "public-model")
			ctx2, _ := svc.withOpenAIUserAffinity(auth, &group, session2, "public-model")
			var wg sync.WaitGroup
			errs := make(chan error, 2)
			wg.Add(2)
			go func() { defer wg.Done(); errs <- svc.BindStickySessionAfterProfitAdmission(ctx1, &group, session1, 1) }()
			go func() { defer wg.Done(); errs <- svc.BindStickySessionAfterProfitAdmission(ctx2, &group, session2, 2) }()
			wg.Wait()
			close(errs)
			for err := range errs {
				require.NoError(t, err)
			}
			owner, err := cache.GetSessionAccountID(auth, group, openAIUserAffinityKey(7, group, "public-model"))
			require.NoError(t, err)
			require.Contains(t, []int64{1, 2}, owner)
			if sameSession {
				sessionOwner, err := cache.GetSessionAccountID(auth, group, "openai:first")
				require.NoError(t, err)
				require.Equal(t, owner, sessionOwner)
			}
			// A response selected before the winner committed cannot restore its owner.
			require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx1, &group, session1, 1))
			persisted, err := cache.GetSessionAccountID(auth, group, openAIUserAffinityKey(7, group, "public-model"))
			require.NoError(t, err)
			require.Equal(t, owner, persisted)
		})
	}
}

func TestOpenAIUserAffinityMigrationKeepsOtherConversationsAndDoesNotBounce(t *testing.T) {
	cache := &userAffinityTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	group := int64(43)
	auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
	key := openAIUserAffinityKey(7, group, "public-model")
	for _, k := range []string{key, "openai:old", "openai:moving"} {
		require.NoError(t, cache.SetSessionAccountID(auth, group, k, 1, time.Hour))
	}
	stale, _ := svc.withOpenAIUserAffinity(auth, &group, "moving", "public-model")
	migration, _ := svc.withOpenAIUserAffinity(auth, &group, "moving", "public-model")
	require.NoError(t, svc.clearOpenAIStickySessionForRouting(migration, &group, "moving"))
	before, err := cache.GetSessionAccountID(auth, group, "openai:moving")
	require.NoError(t, err)
	require.Equal(t, int64(1), before)
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(migration, &group, "moving", 2))
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(stale, &group, "moving", 1))
	old, _ := svc.withOpenAIUserAffinity(auth, &group, "old", "public-model")
	owner, err := svc.stickySessionAccountIDForRouting(old, &group, "old")
	require.NoError(t, err)
	require.Equal(t, int64(1), owner)
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(old, &group, "old", 1))
	for _, k := range []string{key, "openai:moving"} {
		id, err := cache.GetSessionAccountID(auth, group, k)
		require.NoError(t, err)
		require.Equal(t, int64(2), id)
	}
	newSession, _ := svc.withOpenAIUserAffinity(auth, &group, "new", "public-model")
	owner, err = svc.stickySessionAccountIDForRouting(newSession, &group, "new")
	require.NoError(t, err)
	require.Equal(t, int64(2), owner)
}

func TestOpenAIUserAffinityLegacyBindingCanMigrateToPrimaryKey(t *testing.T) {
	cache := &userAffinityTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	group := int64(43)
	auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
	auth = withOpenAILegacySessionHash(auth, "legacy")
	require.NoError(t, cache.SetSessionAccountID(auth, group, "openai:legacy", 1, time.Hour))
	ctx, _ := svc.withOpenAIUserAffinity(auth, &group, "current", "public-model")
	require.Equal(t, int64(1), openAIUserAffinityFromContext(ctx).sessionAccountID)
	markOpenAIUserAffinityMigration(ctx, 1)
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &group, "current", 2))
	id, err := cache.GetSessionAccountID(auth, group, "openai:current")
	require.NoError(t, err)
	require.Equal(t, int64(2), id)
}

func TestOpenAIUserAffinitySeedsAllSchedulersAndMigratesConfirmedHold(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, session := range []string{"", "new-conversation"} {
			t.Run(mode+"/"+session, func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				group := int64(43)
				cache := &userAffinityTestCache{}
				accounts := []Account{groupQualityRoutingTestAccount(1, group, 100, "healthy"), groupQualityRoutingTestAccount(2, group, 0, "healthy")}
				svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
				svc.cache = cache
				auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
				key := openAIUserAffinityKey(7, group, "public-model")
				require.NoError(t, cache.SetSessionAccountID(auth, group, key, 1, time.Hour))
				selectAndAdmit := func(want int64) {
					sel, _, err := svc.SelectAccountWithScheduler(auth, &group, "", session, "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
					require.NoError(t, err)
					require.NotNil(t, sel)
					require.Equal(t, want, sel.Account.ID)
					require.False(t, sel.PreserveStickyBinding)
					require.NotNil(t, sel.userAffinity)
					before, err := cache.GetSessionAccountID(auth, group, key)
					require.NoError(t, err)
					if want == 2 {
						require.Equal(t, int64(1), before)
					}
					require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ContextWithSelectionProfitGate(auth, sel), &group, session, sel.Account.ID))
					if sel.ReleaseFunc != nil {
						sel.ReleaseFunc()
					}
				}
				selectAndAdmit(1)
				accounts[0].Status = "quality_paused"
				accounts[0].Extra["quality_protection_reason"] = "automatic quality hold"
				selectAndAdmit(2)
				accounts[0].Status = StatusActive
				delete(accounts[0].Extra, "quality_protection_reason")
				accounts[0].Priority = -100
				sel, _, err := svc.SelectAccountWithScheduler(auth, &group, "", session, "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
				require.NoError(t, err)
				require.Equal(t, int64(2), sel.Account.ID)
				if sel.ReleaseFunc != nil {
					sel.ReleaseFunc()
				}
			})
		}
	}
}

func TestOpenAIUserAffinityCapacityAndQualitySpilloverPreserveOwner(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, qualityHeld := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/quality_held=%v", mode, qualityHeld), func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				group := int64(43)
				cache := &userAffinityTestCache{}
				accounts := []Account{groupQualityRoutingTestAccount(1, group, 100, "healthy"), groupQualityRoutingTestAccount(2, group, 0, "healthy")}
				concurrency := schedulerTestConcurrencyCache{acquireResults: map[int64]bool{1: false, 2: true}, waitCounts: map[int64]int{1: 999}, loadMap: map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 100}, 2: {AccountID: 2}}}
				if qualityHeld {
					concurrency.acquireResults[1] = true
				}
				svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, concurrency)
				svc.cache = cache
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeEnabled = true
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeErrorRate = 0.5
				svc.cfg.Gateway.OpenAIScheduler.StickyEscapeTTFTMs = 15000
				svc.cfg.Gateway.Scheduling.StickySessionMaxWaiting = 1
				auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
				if qualityHeld {
					svc.concurrencyService.passiveQualityGate = &qualityHoldTestGate{normal: map[int64]bool{2: true}}
					auth = GroupQualityTrafficContext(auth, GroupQualityTrafficRequest{Key: "user-spillover", GroupID: group, Model: "public-model"})
				}
				key := openAIUserAffinityKey(7, group, "public-model")
				for _, k := range []string{key, "openai:conversation"} {
					require.NoError(t, cache.SetSessionAccountID(auth, group, k, 1, time.Hour))
				}
				sel, _, err := svc.SelectAccountWithScheduler(auth, &group, "", "conversation", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
				require.NoError(t, err)
				require.NotNil(t, sel)
				want := int64(2)
				if mode == "legacy_nonbatch" && !qualityHeld {
					// The bounded non-batch sticky wait remains the owner.
					want = 1
					require.False(t, sel.Acquired)
				}
				require.Equal(t, want, sel.Account.ID)
				if want != 1 {
					require.True(t, sel.PreserveStickyBinding)
				}
				for _, k := range []string{key, "openai:conversation"} {
					id, err := cache.GetSessionAccountID(auth, group, k)
					require.NoError(t, err)
					require.Equal(t, int64(1), id)
				}
				if sel.ReleaseFunc != nil {
					sel.ReleaseFunc()
				}
			})
		}
	}
}

func TestOpenAIUserAffinityManagedTTFTIsSoftButErrorsStillMigrate(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(fmt.Sprint(failed), func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			group := int64(43)
			cache := &userAffinityTestCache{}
			accounts := []Account{groupQualityRoutingTestAccount(1, group, 100, "healthy"), groupQualityRoutingTestAccount(2, group, 0, "healthy")}
			svc := groupQualityRoutingTestService("advanced", accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
			svc.cache = cache
			svc.openaiAccountStats = newOpenAIAccountRuntimeStats()
			slow := 60000
			for range 6 {
				svc.openaiAccountStats.report(1, !failed, &slow)
			}
			auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
			key := openAIUserAffinityKey(7, group, "public-model")
			for _, k := range []string{key, "openai:conversation"} {
				require.NoError(t, cache.SetSessionAccountID(auth, group, k, 1, time.Hour))
			}
			sel, _, err := svc.SelectAccountWithScheduler(auth, &group, "", "conversation", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
			require.NoError(t, err)
			require.NotNil(t, sel)
			want := int64(1)
			if failed {
				want = 2
			}
			require.Equal(t, want, sel.Account.ID)
			require.False(t, sel.PreserveStickyBinding)
			require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ContextWithSelectionProfitGate(auth, sel), &group, "conversation", sel.Account.ID))
			id, err := cache.GetSessionAccountID(auth, group, key)
			require.NoError(t, err)
			require.Equal(t, want, id)
			if sel.ReleaseFunc != nil {
				sel.ReleaseFunc()
			}
		})
	}
}

func TestOpenAIUserAffinityDoesNotOverrideImmovableResponseOrLeakOnReselection(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			group := int64(43)
			cache := &userAffinityTestCache{}
			accounts := []Account{groupQualityRoutingTestAccount(1, group, 100, "healthy"), groupQualityRoutingTestAccount(2, group, 0, "healthy")}
			for i := range accounts {
				accounts[i].Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			}
			svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
			svc.cache = cache
			svc.cfg.Gateway.OpenAIWS = newSchedulerTestOpenAIWSV2Config().Gateway.OpenAIWS
			auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
			key := openAIUserAffinityKey(7, group, "public-model")
			require.NoError(t, cache.SetSessionAccountID(auth, group, key, 2, time.Hour))
			inherited, _ := svc.withOpenAIUserAffinity(auth, &group, "previous-conversation", "public-model")
			require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(auth, group, "resp_immovable", 1, time.Hour))
			sel, decision, err := svc.SelectAccountWithScheduler(inherited, &group, "resp_immovable", "conversation", "public-model", nil, OpenAIUpstreamTransportResponsesWebsocketV2, false)
			require.NoError(t, err)
			require.NotNil(t, sel)
			require.Equal(t, int64(1), sel.Account.ID)
			require.Equal(t, openAIAccountScheduleLayerPreviousResponse, decision.Layer)
			require.Nil(t, sel.userAffinity)
			if sel.ReleaseFunc != nil {
				sel.ReleaseFunc()
			}
			id, err := cache.GetSessionAccountID(auth, group, key)
			require.NoError(t, err)
			require.Equal(t, int64(2), id)
		})
	}
}

func TestOpenAIUserAffinityProfitSpilloverDoesNotBindNewSession(t *testing.T) {
	cache := &userAffinityTestCache{}
	svc := &OpenAIGatewayService{cache: cache}
	group := int64(43)
	auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
	key := openAIUserAffinityKey(7, group, "public-model")
	require.NoError(t, cache.SetSessionAccountID(auth, group, key, 1, time.Hour))
	ctx, _ := svc.withOpenAIUserAffinity(auth, &group, "new-session", "public-model")
	ctx = context.WithValue(ctx, openAIProfitControlGateCtxKey{}, &openAIProfitControlGate{groupID: group, threshold: 0.5})
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &group, "new-session", 2))
	id, err := cache.GetSessionAccountID(auth, group, key)
	require.NoError(t, err)
	require.Equal(t, int64(1), id)
	_, err = cache.GetSessionAccountID(auth, group, "openai:new-session")
	require.ErrorIs(t, err, ErrStickySessionNotFound)
	markOpenAIUserAffinityMigration(ctx, 1)
	require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ctx, &group, "new-session", 2))
	id, err = cache.GetSessionAccountID(auth, group, key)
	require.NoError(t, err)
	require.Equal(t, int64(2), id)
}

func TestOpenAIUserAffinityCompactCapabilityCannotMigrateOrdinaryOwner(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			group := int64(43)
			cache := &userAffinityTestCache{}
			accounts := []Account{groupQualityRoutingTestAccount(1, group, 100, "healthy"), groupQualityRoutingTestAccount(2, group, 0, "healthy")}
			accounts[0].Extra["openai_compact_supported"] = false
			accounts[1].Extra["openai_compact_supported"] = true
			svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
			svc.cache = cache
			auth := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101, UserID: 7})
			key := openAIUserAffinityKey(7, group, "public-model")
			require.NoError(t, cache.SetSessionAccountID(auth, group, key, 1, time.Hour))
			inherited, _ := svc.withOpenAIUserAffinity(auth, &group, "prior-selection", "public-model")
			sel, _, err := svc.SelectAccountWithScheduler(inherited, &group, "", "compact-session", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, true)
			require.NoError(t, err)
			require.NotNil(t, sel)
			require.Equal(t, int64(2), sel.Account.ID)
			require.Nil(t, sel.userAffinity)
			require.NoError(t, svc.BindStickySessionAfterProfitAdmission(ContextWithSelectionProfitGate(inherited, sel), &group, "compact-session", sel.Account.ID))
			if sel.ReleaseFunc != nil {
				sel.ReleaseFunc()
			}
			owner, err := cache.GetSessionAccountID(auth, group, key)
			require.NoError(t, err)
			require.Equal(t, int64(1), owner)
			sel, _, err = svc.SelectAccountWithScheduler(auth, &group, "", "ordinary-new-session", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
			require.NoError(t, err)
			require.NotNil(t, sel)
			require.Equal(t, int64(1), sel.Account.ID)
			if sel.ReleaseFunc != nil {
				sel.ReleaseFunc()
			}
		})
	}
}
