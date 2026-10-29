package service

import (
	"context"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

type qualityHoldTestRepo struct {
	schedulerGroupAwareOpenAIAccountRepo
}
type qualityHoldGroupTestRepo struct{ GroupRepository }

func (qualityHoldGroupTestRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return &Group{ID: id, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}, nil
}
func (r qualityHoldTestRepo) ListByGroup(ctx context.Context, id int64) ([]Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(ctx, id, PlatformOpenAI)
}

type qualityHoldTestGate struct {
	PassiveQualityRepository
	normal   map[int64]bool
	groups   []int64
	accounts []int64
}

func (g *qualityHoldTestGate) AcquirePassiveQuality(ctx context.Context, id int64, model, lease string) (bool, bool, error) {
	identity, _ := GroupQualityTrafficRequestFromContext(ctx)
	g.groups = append(g.groups, identity.GroupID)
	g.accounts = append(g.accounts, id)
	return g.normal[id], false, nil
}
func (*qualityHoldTestGate) ReleasePassiveQuality(context.Context, string, bool) error { return nil }

func qualityHoldTestAccount(id, groupID int64, priority int, activeHold bool) Account {
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

func qualityHoldService(mode string, accounts []Account, gate *qualityHoldTestGate) *OpenAIGatewayService {
	svc := groupQualityRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
	svc.accountRepo = qualityHoldTestRepo{schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}
	svc.schedulerSnapshot = &SchedulerSnapshotService{accountRepo: svc.accountRepo, groupRepo: qualityHoldGroupTestRepo{}}
	svc.concurrencyService.passiveQualityGate = gate
	svc.cfg.Gateway.OpenAIWS = newSchedulerTestOpenAIWSV2Config().Gateway.OpenAIWS
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	return svc
}

func TestQualityHeldAccountsNeverReceiveCustomerRecoveryTraffic(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, transport := range []OpenAIUpstreamTransport{OpenAIUpstreamTransportHTTPSSE, OpenAIUpstreamTransportResponsesWebsocketV2} {
			for _, healthy := range []bool{false, true} {
				t.Run(mode+"/"+string(transport)+map[bool]string{false: "/all_held", true: "/healthy_peer"}[healthy], func(t *testing.T) {
					resetOpenAIAdvancedSchedulerSettingCacheForTest()
					defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
					group := int64(43)
					accounts := []Account{qualityHoldTestAccount(1, group, 0, false), qualityHoldTestAccount(2, group, 0, true)}
					allowed := map[int64]bool{1: true, 2: true}
					if healthy {
						account := groupQualityRoutingTestAccount(3, group, 100, "healthy")
						account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
						accounts = append(accounts, account)
						allowed[3] = true
					}
					gate := &qualityHoldTestGate{normal: allowed}
					gateway := qualityHoldService(mode, accounts, gate)
					ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: group, Model: "public-model"})
					selection, _, err := gateway.SelectAccountWithScheduler(ctx, &group, "", "", "public-model", nil, transport, false)
					if healthy {
						require.NoError(t, err)
						require.NotNil(t, selection)
						require.True(t, selection.Acquired)
						require.EqualValues(t, 3, selection.Account.ID)
						selection.ReleaseFunc()
					} else {
						require.ErrorIs(t, err, ErrNoAvailableAccounts)
						require.Nil(t, selection)
					}
					require.NotContains(t, gate.accounts, int64(1))
					require.NotContains(t, gate.accounts, int64(2))
					require.Equal(t, "quality_paused", accounts[1].Status)
				})
			}
		}
	}
}

func TestIndependentStatisticsTrialKeepsResponseOwnerAndFinalAdmission(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		for _, allowed := range []bool{false, true} {
			t.Run(mode+map[bool]string{false: "/denied", true: "/allowed"}[allowed], func(t *testing.T) {
				resetOpenAIAdvancedSchedulerSettingCacheForTest()
				defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
				group := int64(43)
				account := groupQualityRoutingTestAccount(1, group, 0, "healthy")
				account.Extra["quality_protection_trial"] = true
				account.Extra["openai_apikey_responses_websockets_v2_enabled"] = true
				gateway := qualityHoldService(mode, []Account{account}, &qualityHoldTestGate{normal: map[int64]bool{1: true}})
				gateway.openaiWSStateStore = NewOpenAIWSStateStore(nil)
				finalChecks := 0
				gateway.concurrencyService.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(_ context.Context, id int64) (bool, error) {
					require.EqualValues(t, 1, id)
					finalChecks++
					return allowed, nil
				}))
				ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "stats", GroupID: group, Model: "public-model"})
				store := gateway.getOpenAIWSStateStore()
				require.NoError(t, store.BindResponseAccount(ctx, group, "resp_statistics_trial", 1, time.Hour))
				selection, _, err := gateway.SelectAccountWithSchedulerForCapability(ctx, &group, "resp_statistics_trial", "", "public-model", nil, OpenAIUpstreamTransportHTTPSSE, OpenAIEndpointCapabilityResponses, false, false, false)
				require.Greater(t, finalChecks, 0, "independent statistics trial must reach its authoritative final gate")
				if allowed {
					require.NoError(t, err)
					require.NotNil(t, selection)
					require.True(t, selection.Acquired)
					require.EqualValues(t, 1, selection.Account.ID)
					selection.ReleaseFunc()
				} else {
					require.ErrorIs(t, err, ErrAccountAdmissionDenied)
					require.Nil(t, selection, "denied trial must not be queued as a concurrency wait")
				}
				owner, err := store.GetResponseAccount(ctx, group, "resp_statistics_trial")
				require.NoError(t, err)
				require.EqualValues(t, 1, owner)
			})
		}
	}
}
