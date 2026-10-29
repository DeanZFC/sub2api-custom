package service

import (
	"context"
	"github.com/Wei-Shaw/sub2api/internal/config"
)

func activeRoutingTestAccount(id, groupID int64, priority int) Account {
	return Account{ID: id, Platform: PlatformOpenAI, Type: AccountTypeAPIKey, Status: StatusActive, Schedulable: true, Concurrency: 8, Priority: priority, GroupIDs: []int64{groupID}, Extra: map[string]any{}}
}
func activeRoutingTestService(mode string, accounts []Account, cache *schedulerTestGatewayCache, concurrencyCache ConcurrencyCache) *OpenAIGatewayService {
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

type activeHoldTestRepo struct {
	schedulerGroupAwareOpenAIAccountRepo
}
type activeHoldGroupTestRepo struct{ GroupRepository }

func (activeHoldGroupTestRepo) GetByIDLite(_ context.Context, id int64) (*Group, error) {
	return &Group{ID: id, Platform: PlatformOpenAI, Status: StatusActive, Hydrated: true}, nil
}
func (r activeHoldTestRepo) ListByGroup(ctx context.Context, id int64) ([]Account, error) {
	return r.ListSchedulableByGroupIDAndPlatform(ctx, id, PlatformOpenAI)
}

type activeHoldTestGate struct {
	normal        map[int64]bool
	accountGroups map[int64]int64
	groups        []int64
}

func (g *activeHoldTestGate) CheckAccountAdmission(_ context.Context, id int64) (bool, error) {
	g.groups = append(g.groups, g.accountGroups[id])
	if !g.normal[id] {
		return false, ErrAccountQualityHeld
	}
	return true, nil
}
func activeHoldService(mode string, accounts []Account, gate *activeHoldTestGate) *OpenAIGatewayService {
	svc := activeRoutingTestService(mode, accounts, &schedulerTestGatewayCache{}, schedulerTestConcurrencyCache{})
	svc.accountRepo = activeHoldTestRepo{schedulerGroupAwareOpenAIAccountRepo{schedulerTestOpenAIAccountRepo{accounts: accounts}}}
	svc.schedulerSnapshot = &SchedulerSnapshotService{accountRepo: svc.accountRepo, groupRepo: activeHoldGroupTestRepo{}}
	gate.accountGroups = make(map[int64]int64)
	for _, a := range accounts {
		if len(a.GroupIDs) > 0 {
			gate.accountGroups[a.ID] = a.GroupIDs[0]
		}
	}
	svc.concurrencyService.SetAccountAdmissionGate(gate)
	svc.cfg.Gateway.OpenAIWS = newSchedulerTestOpenAIWSV2Config().Gateway.OpenAIWS
	svc.cfg.Gateway.OpenAIWS.LBTopK = 1
	svc.cfg.Gateway.OpenAIWS.SchedulerScoreWeights.Priority = 1
	return svc
}
