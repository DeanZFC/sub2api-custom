package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/stretchr/testify/require"
)

type requestSupplyFallbackTestGate struct {
	groupQualityFallbackTestGate
	live   []*Account
	checks int
}

func (g *requestSupplyFallbackTestGate) AcquireGroupQualityTraffic(ctx context.Context, accountID int64, model, lease string) (bool, bool, error) {
	if _, fallback := GroupQualityFallbackFromContext(ctx); fallback {
		g.checks++
		supply, err := GroupQualityFallbackHasRequestSupply(ctx, g.live)
		if err != nil || supply {
			return false, false, err
		}
	}
	return g.groupQualityFallbackTestGate.AcquireGroupQualityTraffic(ctx, accountID, model, lease)
}

func TestGroupQualityFallbackIgnoresSupplyUnusableForRequest(t *testing.T) {
	for _, scenario := range []string{"non owner", "wrong transport", "excluded", "full concurrency"} {
		t.Run(scenario, func(t *testing.T) {
			groupID := int64(43)
			owner := groupQualityFallbackTestAccount(1, groupID, 100, true)
			peer := groupQualityRoutingTestAccount(2, groupID, 0, "healthy")
			peer.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
			gate := &requestSupplyFallbackTestGate{groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true}, live: []*Account{&peer}}
			svc := groupQualityFallbackService("legacy", []Account{owner, peer}, &gate.groupQualityFallbackTestGate)
			svc.concurrencyService.passiveQualityGate = gate
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			switch scenario {
			case "non owner":
				req.PreviousResponseID = "resp_owner"
				req.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2
				require.NoError(t, svc.getOpenAIWSStateStore().BindResponseAccount(ctx, groupID, req.PreviousResponseID, owner.ID, time.Hour))
			case "wrong transport":
				peer.Extra["openai_apikey_responses_websockets_v2_enabled"] = false
				req.RequiredTransport = OpenAIUpstreamTransportResponsesWebsocketV2
			case "excluded":
				req.ExcludedIDs = map[int64]struct{}{peer.ID: {}}
			case "full concurrency":
				svc.concurrencyService.cache = schedulerTestConcurrencyCache{loadMap: map[int64]*AccountLoadInfo{
					peer.ID: {AccountID: peer.ID, CurrentConcurrency: peer.Concurrency, LoadRate: 100},
				}}
			}
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.NoError(t, err)
			require.NotNil(t, selection)
			require.Equal(t, owner.ID, selection.Account.ID)
			require.Equal(t, 1, gate.checks, "live admission must repeat the request-specific supply check")
			selection.ReleaseFunc()
		})
	}
}

func TestGroupQualityFallbackRechecksNormalCapacityAtAdmission(t *testing.T) {
	groupID := int64(43)
	accounts := []Account{groupQualityFallbackTestAccount(1, groupID, 0, true)}
	peer := groupQualityRoutingTestAccount(2, groupID, 0, "healthy")
	gate := &requestSupplyFallbackTestGate{groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true}, live: []*Account{&peer}}
	svc := groupQualityFallbackService("legacy", accounts, &gate.groupQualityFallbackTestGate)
	svc.concurrencyService.passiveQualityGate = gate
	ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
	selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
	require.Nil(t, selection)
	require.ErrorIs(t, err, ErrAccountAdmissionDenied)
	require.NotErrorIs(t, err, ErrGroupQualityTrafficBudget)
	require.Equal(t, 1, gate.checks, "a newly usable live account closes fallback admission")
}

func TestGroupQualityFallbackBusyCapacityDoesNotInventBudgetDenial(t *testing.T) {
	for _, scenario := range []string{"load full", "atomic acquisition full", "hard admission"} {
		t.Run(scenario, func(t *testing.T) {
			groupID := int64(43)
			account := groupQualityFallbackTestAccount(1, groupID, 0, true)
			gate := &groupQualityFallbackTestGate{recovery: true}
			svc := groupQualityFallbackService("legacy", []Account{account}, gate)
			cache := schedulerTestConcurrencyCache{}
			switch scenario {
			case "load full":
				cache.loadMap = map[int64]*AccountLoadInfo{account.ID: {AccountID: account.ID, CurrentConcurrency: account.Concurrency, LoadRate: 100}}
			case "atomic acquisition full":
				cache.acquireResults = map[int64]bool{account.ID: false}
			case "hard admission":
				svc.concurrencyService.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) { return false, nil }))
			}
			svc.concurrencyService.cache = cache
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.Nil(t, selection)
			require.NotErrorIs(t, err, ErrGroupQualityTrafficBudget)
			if scenario == "hard admission" {
				require.ErrorIs(t, err, ErrAccountAdmissionDenied)
			} else {
				require.ErrorIs(t, err, ErrNoAvailableAccounts)
				require.Empty(t, gate.calls)
			}
		})
	}
}

func TestGroupQualityFallbackSupplyUsesFreshLoadAndRealConcurrency(t *testing.T) {
	groupID := int64(43)
	peer := groupQualityRoutingTestAccount(2, groupID, 0, "healthy")
	loadFactor := 100
	peer.Concurrency, peer.LoadFactor, peer.ProxyIDs = 2, &loadFactor, []int64{7, 8}
	peer.Proxies = []*Proxy{{ID: 7, Status: StatusActive}, {ID: 8, Status: StatusActive}}
	loads := map[int64]*AccountLoadInfo{peer.ID: {AccountID: peer.ID, CurrentConcurrency: 0}}
	svc := groupQualityFallbackService("legacy", nil, &groupQualityFallbackTestGate{recovery: true})
	svc.concurrencyService.cache = schedulerTestConcurrencyCache{loadMap: loads}
	svc.concurrencyService.SetAccountLoadBatchCacheTTL(time.Hour)
	ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
	_, err := svc.concurrencyService.GetAccountsLoadBatch(ctx, buildOpenAIAccountLoadRequest([]*Account{&peer}))
	require.NoError(t, err)
	loads[peer.ID] = &AccountLoadInfo{AccountID: peer.ID, CurrentConcurrency: 4, LoadRate: 2}
	scheduler := &defaultOpenAIAccountScheduler{service: svc}
	check := svc.groupQualityFallbackSupplyChecker(ctx, scheduler, req, 0)
	available, err := check(ctx, []*Account{&peer})
	require.NoError(t, err)
	require.False(t, available, "full physical slots cannot be hidden by a large load factor or cached load")
	loads[peer.ID] = &AccountLoadInfo{AccountID: peer.ID, CurrentConcurrency: 3, LoadRate: 1}
	available, err = check(ctx, []*Account{&peer})
	require.NoError(t, err)
	require.True(t, available, "a proxy pool contributes its actual ordinary concurrency")
	loads[peer.ID] = &AccountLoadInfo{AccountID: peer.ID, CurrentConcurrency: 2}
	peer.Proxies[1].Status = StatusDisabled
	available, err = check(ctx, []*Account{&peer})
	require.NoError(t, err)
	require.False(t, available, "a disabled proxy cannot invent free capacity")
	peer.Proxies[1].Status = StatusActive
	expired := time.Now().Add(-time.Minute)
	peer.Proxies[1].ExpiresAt = &expired
	available, err = check(ctx, []*Account{&peer})
	require.NoError(t, err)
	require.False(t, available, "an expired proxy cannot invent free capacity")
	peer.Proxies[0].Status = StatusDisabled
	loads[peer.ID] = &AccountLoadInfo{AccountID: peer.ID}
	available, err = check(ctx, []*Account{&peer})
	require.NoError(t, err)
	require.False(t, available, "a pool with no live proxy is not normal supply")
}

func TestGroupQualityFallbackUnknownLoadDoesNotVetoEmergency(t *testing.T) {
	for _, missing := range []bool{false, true} {
		t.Run(map[bool]string{false: "load error", true: "missing row"}[missing], func(t *testing.T) {
			groupID := int64(43)
			peer := groupQualityRoutingTestAccount(2, groupID, 0, "healthy")
			cache := schedulerTestConcurrencyCache{loadBatchErr: errors.New("load unavailable")}
			if missing {
				cache = schedulerTestConcurrencyCache{skipDefaultLoad: true, loadMap: map[int64]*AccountLoadInfo{}}
			}
			svc := groupQualityFallbackService("legacy", nil, &groupQualityFallbackTestGate{})
			svc.concurrencyService.cache = cache
			ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
			check := svc.groupQualityFallbackSupplyChecker(ctx, &defaultOpenAIAccountScheduler{service: svc}, req, 0)
			available, err := check(ctx, []*Account{&peer})
			require.NoError(t, err)
			require.False(t, available, "unknown capacity cannot prove normal supply; final atomic fallback acquisition still applies")
		})
	}
}

func TestGroupQualityFallbackSupplyScopeReplaysAndRefreshesPerTurn(t *testing.T) {
	groupID := int64(43)
	owner := groupQualityFallbackTestAccount(1, groupID, 0, true)
	owner.Credentials = map[string]any{"model_mapping": map[string]any{"provider-model": "upstream"}}
	gate := &groupQualityFallbackTestGate{recovery: true}
	svc := groupQualityFallbackService("legacy", []Account{owner}, gate)
	ctx, req := groupQualityAffinityRequest(&groupID, "public-model", "")
	ctx = context.WithValue(ctx, ctxkey.RequestedPublicModel, "public-model")
	req.RequestedModel = "provider-model"
	selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
	require.NoError(t, err)
	defer selection.ReleaseFunc()
	replayed := ContextWithSelectionProfitGate(ctx, selection)
	require.Equal(t, "provider-model", GroupQualityFallbackAdmissionModel(replayed, "public-model"))
	peer := groupQualityRoutingTestAccount(2, groupID, 0, "healthy")
	peer.Credentials = map[string]any{"model_mapping": map[string]any{"provider-model": "upstream"}}
	available, err := GroupQualityFallbackHasRequestSupply(replayed, []*Account{&peer})
	require.NoError(t, err)
	require.True(t, available, "initial live peer is checked against the forwarded model")

	// A later WS turn may change both the public name and its channel mapping,
	// but the established upstream credential remains the only valid owner.
	next := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "next-turn", GroupID: groupID, Model: "next-public"})
	next = context.WithValue(next, ctxkey.RequestedPublicModel, "next-public")
	next = ContextWithSelectionProfitGate(next, selection)
	marker, ok := GroupQualityFallbackFromContext(next)
	require.True(t, ok)
	marker.Model = "next-public"
	next = WithGroupQualityFallback(next, marker)
	next = WithGroupQualityFallbackAdmissionModel(next, "next-provider")
	owner.Status = StatusActive
	owner.Extra = map[string]any{}
	owner.Credentials = map[string]any{"model_mapping": map[string]any{"next-provider": "upstream"}}
	available, err = GroupQualityFallbackHasRequestSupply(next, []*Account{&owner})
	require.NoError(t, err)
	require.True(t, available, "next turn uses its fresh forwarded model and can exit recovery on its owner")
	peer.Credentials = owner.Credentials
	available, err = GroupQualityFallbackHasRequestSupply(next, []*Account{&peer})
	require.NoError(t, err)
	require.False(t, available, "a healthy peer cannot veto a connection-bound owner's recovery")
	cleared := WithoutGroupQualityFallback(replayed)
	require.Equal(t, "public-model", GroupQualityFallbackAdmissionModel(cleared, "public-model"), "ordinary attempts cannot inherit the old scope")
}
