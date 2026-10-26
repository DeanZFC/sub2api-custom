package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

type groupQualityAffinityTestCache struct {
	schedulerTestGatewayCache
	readErr, writeErr, refreshErr error
	writtenTTL, refreshedTTL      time.Duration
}

func (c *groupQualityAffinityTestCache) GetSessionAccountID(ctx context.Context, groupID int64, key string) (int64, error) {
	if c.readErr != nil {
		return 0, c.readErr
	}
	return c.schedulerTestGatewayCache.GetSessionAccountID(ctx, groupID, key)
}

func (c *groupQualityAffinityTestCache) SetSessionAccountID(ctx context.Context, groupID int64, key string, accountID int64, ttl time.Duration) error {
	if strings.HasPrefix(key, "openai:group-quality-fallback:") {
		c.writtenTTL = ttl
	}
	if c.writeErr != nil {
		return c.writeErr
	}
	return c.schedulerTestGatewayCache.SetSessionAccountID(ctx, groupID, key, accountID, ttl)
}

func (c *groupQualityAffinityTestCache) RefreshSessionTTL(_ context.Context, _ int64, key string, ttl time.Duration) error {
	if strings.HasPrefix(key, "openai:group-quality-fallback:") {
		c.refreshedTTL = ttl
	}
	return c.refreshErr
}

func groupQualityAffinityRequest(groupID *int64, model, session string) (context.Context, OpenAIAccountScheduleRequest) {
	ctx := WithAPIKeyForUsageContext(context.Background(), &APIKey{ID: 101})
	return GroupQualityTrafficContext(ctx, GroupQualityTrafficRequest{Key: "affinity-request", GroupID: *groupID, Model: model}), OpenAIAccountScheduleRequest{
		GroupID: groupID, Platform: PlatformOpenAI, RequestedModel: model, SessionHash: session, RequiredTransport: OpenAIUpstreamTransportHTTPSSE,
	}
}

func TestGroupQualityFallbackAffinitySurvivesScoreAndLoadChanges(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityFallbackTestAccount(2, groupID, 100, true)}
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService(mode, accounts, gate)
			cache := &groupQualityAffinityTestCache{schedulerTestGatewayCache: schedulerTestGatewayCache{sessionBindings: map[string]int64{"openai:conversation": 2}}}
			svc.cache = cache
			selectAccount := func() *AccountSelectionResult {
				ctx, _ := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
				selection, decision, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "conversation", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
				require.NoError(t, err)
				require.Equal(t, "group_quality_fallback", decision.Layer)
				require.True(t, selection.PreserveStickyBinding)
				selection.ReleaseFunc()
				return selection
			}
			require.Equal(t, int64(1), selectAccount().Account.ID, "first fallback still uses score, preserving the original normal owner")
			require.Equal(t, 30*time.Minute, cache.writtenTTL)
			accounts[0].Priority, accounts[1].Priority = 200, 0
			svc.concurrencyService.cache = schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{1: {AccountID: 1, LoadRate: 75, CurrentConcurrency: 6, WaitingCount: 3}}}
			require.Equal(t, int64(1), selectAccount().Account.ID, "ordinary ranking and load changes must not migrate the fallback session")
			require.Equal(t, 30*time.Minute, cache.refreshedTTL)
			require.Equal(t, int64(2), cache.sessionBindings["openai:conversation"])
			require.Empty(t, cache.deletedSessions)
		})
	}
}

func TestGroupQualityFallbackAffinityIsolatedByAPIKeyGroupModelAndSession(t *testing.T) {
	groupID := int64(43)
	otherGroup := int64(44)
	accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityFallbackTestAccount(2, groupID, 100, true)}
	for i := range accounts {
		accounts[i].GroupIDs = []int64{groupID, otherGroup}
	}
	svc := groupQualityFallbackService("legacy", accounts, &groupQualityFallbackTestGate{recovery: true})
	selectAccount := func(apiKeyID int64, group *int64, model, session string) int64 {
		ctx, req := groupQualityAffinityRequest(group, model, session)
		ctx = WithAPIKeyForUsageContext(ctx, &APIKey{ID: apiKeyID})
		selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
		require.NoError(t, err)
		selection.ReleaseFunc()
		return selection.Account.ID
	}
	require.Equal(t, int64(1), selectAccount(101, &groupID, "public-model", "conversation"))
	accounts[0].Priority, accounts[1].Priority = 200, 0
	for _, scope := range []struct {
		name, model, session string
		apiKeyID             int64
		group                *int64
	}{
		{"other API key", "public-model", "conversation", 102, &groupID},
		{"other model", "other-model", "conversation", 101, &groupID},
		{"other session", "public-model", "other-conversation", 101, &groupID},
		{"other group", "public-model", "conversation", 101, &otherGroup},
	} {
		t.Run(scope.name, func(t *testing.T) {
			require.Equal(t, int64(2), selectAccount(scope.apiKeyID, scope.group, scope.model, scope.session))
		})
	}
	require.Equal(t, int64(1), selectAccount(101, &groupID, "public-model", "conversation"))
}

type groupQualityAffinityDeniedGate struct {
	groupQualityFallbackTestGate
	denied int64
}

func (g *groupQualityAffinityDeniedGate) AcquireGroupQualityTraffic(ctx context.Context, accountID int64, model, lease string) (bool, bool, error) {
	allowed, reserved, err := g.groupQualityFallbackTestGate.AcquireGroupQualityTraffic(ctx, accountID, model, lease)
	if accountID == g.denied {
		return false, false, nil
	}
	return allowed, reserved, err
}

func TestGroupQualityFallbackAffinityRechecksAvailability(t *testing.T) {
	for _, reason := range []string{"disabled", "excluded", "full snapshot", "full at acquisition", "live lease denied"} {
		t.Run(reason, func(t *testing.T) {
			groupID := int64(43)
			accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 100, true), groupQualityFallbackTestAccount(2, groupID, 0, true)}
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService("legacy", accounts, gate)
			key := groupQualityFallbackAffinityKey(101, groupID, "public-model", "conversation")
			cache := &schedulerTestGatewayCache{sessionBindings: map[string]int64{key: 1, "openai:conversation": 1}}
			svc.cache = cache
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
			var acquired, released []int64
			cc := schedulerTestConcurrencyCache{acquiredIDs: &acquired, releasedIDs: &released}
			switch reason {
			case "disabled":
				accounts[0].Schedulable = false
			case "excluded":
				req.ExcludedIDs = map[int64]struct{}{1: {}}
			case "full snapshot":
				cc.loadMap = map[int64]*AccountLoadInfo{1: {AccountID: 1, CurrentConcurrency: accounts[0].Concurrency}}
			case "full at acquisition":
				cc.acquireResults = map[int64]bool{1: false}
			case "live lease denied":
				svc.concurrencyService.passiveQualityGate = &groupQualityAffinityDeniedGate{groupQualityFallbackTestGate: *gate, denied: 1}
			}
			svc.concurrencyService.cache = cc
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.NoError(t, err)
			require.Equal(t, int64(2), selection.Account.ID)
			selection.ReleaseFunc()
			require.Equal(t, int64(2), cache.sessionBindings[key], "a successfully acquired replacement becomes the next preference")
			require.Equal(t, int64(1), cache.sessionBindings["openai:conversation"])
			if reason == "disabled" || reason == "excluded" || reason == "full snapshot" {
				require.Equal(t, []int64{2}, acquired)
			} else {
				require.Equal(t, []int64{1, 2}, acquired)
			}
			if reason == "live lease denied" {
				require.Equal(t, []int64{1, 2}, released, "denied fallback must return its ordinary concurrency slot")
			}
		})
	}
}

func TestGroupQualityFallbackAffinityKeepsTTFTSoftButRetainsErrorEscape(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "slow successful account", true: "slow account with errors"}[failed], func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			groupID := int64(43)
			accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 100, true), groupQualityFallbackTestAccount(2, groupID, 0, true)}
			svc := groupQualityFallbackService("advanced", accounts, &groupQualityFallbackTestGate{recovery: true})
			svc.openaiAccountStats = newOpenAIAccountRuntimeStats()
			slowTTFT := 60000
			for range 6 {
				svc.openaiAccountStats.report(1, !failed, &slowTTFT)
			}
			svc.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{groupQualityFallbackAffinityKey(101, groupID, "public-model", "conversation"): 1}}
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.NoError(t, err)
			want := int64(1)
			if failed {
				want = 2
			}
			require.Equal(t, want, selection.Account.ID)
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityFallbackAffinityCannotOverridePreviousResponseOwner(t *testing.T) {
	groupID := int64(43)
	svc := groupQualityFallbackService("legacy", []Account{groupQualityFallbackTestAccount(1, groupID, 100, true), groupQualityFallbackTestAccount(2, groupID, 0, true)}, &groupQualityFallbackTestGate{recovery: true})
	svc.cache = &schedulerTestGatewayCache{sessionBindings: map[string]int64{groupQualityFallbackAffinityKey(101, groupID, "public-model", "conversation"): 2}}
	ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
	req.PreviousResponseID = "resp_owner"
	req.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2
	require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, req.PreviousResponseID, 1, time.Hour))
	selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
	require.NoError(t, err)
	require.Equal(t, int64(1), selection.Account.ID)
	require.True(t, selection.PreserveStickyBinding)
	selection.ReleaseFunc()
}

func TestGroupQualityFallbackAffinityYieldsToHealthySupply(t *testing.T) {
	groupID := int64(43)
	accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true), groupQualityRoutingTestAccount(2, groupID, 100, "healthy")}
	gate := &groupQualityFallbackTestGate{recovery: true, normal: map[int64]bool{2: true}}
	svc := groupQualityFallbackService("legacy", accounts, gate)
	cache := &groupQualityAffinityTestCache{schedulerTestGatewayCache: schedulerTestGatewayCache{sessionBindings: map[string]int64{
		groupQualityFallbackAffinityKey(101, groupID, "public-model", "conversation"): 1, "openai:conversation": 1,
	}}}
	svc.cache = cache
	ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
	selection, _, err := svc.SelectAccountWithScheduler(ctx, &groupID, "", "conversation", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, false)
	require.NoError(t, err)
	require.Equal(t, int64(2), selection.Account.ID)
	require.Nil(t, selection.groupQualityFallback)
	require.Empty(t, gate.calls)
	selection.ReleaseFunc()
	selection, err = svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
	require.ErrorIs(t, err, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	require.Zero(t, cache.refreshedTTL, "normal supply must not keep old fallback affinity alive")
}

func TestGroupQualityFallbackAffinityCacheFailuresDoNotBlockAvailability(t *testing.T) {
	for _, failure := range []string{"no cache", "read", "write", "refresh", "no session", "missing identity", "zero identity", "negative identity"} {
		t.Run(failure, func(t *testing.T) {
			groupID := int64(43)
			svc := groupQualityFallbackService("legacy", []Account{groupQualityFallbackTestAccount(1, groupID, 100, true), groupQualityFallbackTestAccount(2, groupID, 0, true)}, &groupQualityFallbackTestGate{recovery: true})
			cache := &groupQualityAffinityTestCache{schedulerTestGatewayCache: schedulerTestGatewayCache{sessionBindings: map[string]int64{groupQualityFallbackAffinityKey(101, groupID, "public-model", "conversation"): 1}}}
			svc.cache = cache
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "conversation")
			cacheErr := errors.New("cache unavailable")
			want := int64(2)
			switch failure {
			case "no cache":
				svc.cache = nil
			case "read":
				cache.readErr = cacheErr
			case "write":
				cache.sessionBindings = nil
				cache.writeErr = cacheErr
			case "refresh":
				cache.refreshErr = cacheErr
				want = 1
			case "no session":
				req.SessionHash = ""
			case "missing identity":
				ctx = WithAPIKeyForUsageContext(ctx, nil)
			case "zero identity":
				ctx = WithAPIKeyForUsageContext(ctx, &APIKey{})
			case "negative identity":
				ctx = WithAPIKeyForUsageContext(ctx, &APIKey{ID: -1})
			}
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.NoError(t, err)
			require.Equal(t, want, selection.Account.ID)
			selection.ReleaseFunc()
			if failure == "missing identity" || failure == "zero identity" || failure == "negative identity" {
				require.Zero(t, cache.writtenTTL, "requests without an authenticated key must not create affinity")
				require.Zero(t, cache.refreshedTTL)
			}
		})
	}
}
