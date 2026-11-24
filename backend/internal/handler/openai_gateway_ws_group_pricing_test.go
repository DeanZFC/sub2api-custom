package handler

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/stretchr/testify/require"
)

// 分组调价对已打开的 WebSocket 连接生效：同一条连接的两个 turn 之间由管理端
// 改分组倍率（并按分组失效认证缓存），第二个 turn 必须按新倍率计费、按新售价
// 过利润门；HTTP 请求每次经认证缓存取快照，本来就是这个行为。

const (
	wsGroupPricingGroupID = int64(4201) // 与 runOpenAIResponsesWebSocketUsageLogCase 的调度分组一致
	wsGroupPricingKey     = "sk-ws-group-pricing"
	wsGroupPricingFirst   = `{"type":"response.create","model":"gpt-5.6-sol","stream":false}`
	wsGroupPricingSecond  = `{"type":"response.create","model":"gpt-5.6-sol","stream":false}`
)

// wsGroupPricingAPIKeyRepoStub 只实现认证查询与按分组列 Key（认证缓存失效用）。
type wsGroupPricingAPIKeyRepoStub struct {
	service.APIKeyRepository
	mu      sync.Mutex
	apiKey  service.APIKey
	group   service.Group
	err     error
	lookups int
}

func newWSGroupPricingAPIKeyRepoStub(rate float64) *wsGroupPricingAPIKeyRepoStub {
	groupID := wsGroupPricingGroupID
	return &wsGroupPricingAPIKeyRepoStub{
		apiKey: service.APIKey{
			ID:      1801,
			UserID:  1701,
			Key:     wsGroupPricingKey,
			Status:  service.StatusActive,
			GroupID: &groupID,
			User:    &service.User{ID: 1701, Status: service.StatusActive, Concurrency: 1},
		},
		group: service.Group{
			ID:             groupID,
			Name:           "ws-group-pricing",
			Platform:       service.PlatformOpenAI,
			Status:         service.StatusActive,
			RateMultiplier: rate,
			Hydrated:       true,
		},
	}
}

// Install the same authentication lookup used by the upgraded connection.
func installWSFixtureKey(t *testing.T, h *OpenAIGatewayHandler, key *service.APIKey) *service.APIKey {
	t.Helper()
	repo := newWSGroupPricingAPIKeyRepoStub(1)
	if key.Group != nil {
		repo.group = *key.Group
	}
	repo.group.ID = *key.GroupID
	repo.group.Status, repo.group.Hydrated = service.StatusActive, true
	repo.apiKey = *key
	repo.apiKey.Key, repo.apiKey.Status = wsGroupPricingKey, service.StatusActive
	repo.apiKey.UserID = key.User.ID
	h.apiKeyService = newWSGroupPricingAPIKeyService(repo)
	loaded, err := h.apiKeyService.GetByKey(context.Background(), wsGroupPricingKey)
	require.NoError(t, err)
	return loaded
}

func (s *wsGroupPricingAPIKeyRepoStub) GetByKeyForAuth(ctx context.Context, key string) (*service.APIKey, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.lookups++
	if s.err != nil {
		return nil, s.err
	}
	if key != s.apiKey.Key {
		return nil, service.ErrAPIKeyNotFound
	}
	apiKey := s.apiKey
	user := *s.apiKey.User
	apiKey.User = &user
	group := s.group
	apiKey.Group = &group
	return &apiKey, nil
}

func (s *wsGroupPricingAPIKeyRepoStub) ListKeysByGroupID(ctx context.Context, groupID int64) ([]string, error) {
	return []string{s.apiKey.Key}, nil
}

// adminUpdate 模拟管理端改 Key/分组后按分组失效认证缓存（与 admin 分组更新同一入口）。
func (s *wsGroupPricingAPIKeyRepoStub) adminUpdate(apiKeyService *service.APIKeyService, fn func(apiKey *service.APIKey, group *service.Group)) error {
	s.mu.Lock()
	fn(&s.apiKey, &s.group)
	s.mu.Unlock()
	apiKeyService.InvalidateAuthCacheByGroupID(context.Background(), wsGroupPricingGroupID)
	return nil
}

func (s *wsGroupPricingAPIKeyRepoStub) lookupCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lookups
}

func newWSGroupPricingAPIKeyService(repo *wsGroupPricingAPIKeyRepoStub) *service.APIKeyService {
	return service.NewAPIKeyService(repo, nil, nil, nil, nil, nil, &config.Config{})
}

func TestOpenAIResponsesWebSocket_GroupRateChangeAppliesToNextTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModePassthrough, service.OpenAIWSIngressModeCtxPool} {
		t.Run(mode, func(t *testing.T) {
			repo := newWSGroupPricingAPIKeyRepoStub(3.0)
			apiKeyService := newWSGroupPricingAPIKeyService(repo)
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload:     wsGroupPricingFirst,
				secondPayload:    wsGroupPricingSecond,
				ingressMode:      mode,
				apiKeyService:    apiKeyService,
				apiKeyCredential: wsGroupPricingKey,
				afterFirstUpstreamRequest: func(*service.ChannelService) error {
					return repo.adminUpdate(apiKeyService, func(_ *service.APIKey, group *service.Group) { group.RateMultiplier = 0.3 })
				},
			})

			require.Len(t, got.logs, 2)
			require.InDelta(t, 3.0, got.logs[0].RateMultiplier, 1e-12, "turn 1 bills at the rate in force at connect")
			require.InDelta(t, 0.3, got.logs[1].RateMultiplier, 1e-12, "turn 2 must bill at the group rate in force when it starts")
			require.Greater(t, got.logs[0].ActualCost, 0.0)
			require.InDelta(t, got.logs[0].ActualCost/10, got.logs[1].ActualCost, 1e-15)
		})
	}
}

func TestOpenAIResponsesWebSocket_GroupRateChangeReachesProfitGateOnNextTurn(t *testing.T) {
	repo := newWSGroupPricingAPIKeyRepoStub(3.0)
	repo.group.ProfitControlEnabled = true
	repo.group.ProfitMinMargin = 0.1
	apiKeyService := newWSGroupPricingAPIKeyService(repo)
	accountRate := 1.0
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:          wsGroupPricingFirst,
		secondPayload:         wsGroupPricingSecond,
		apiKeyService:         apiKeyService,
		apiKeyCredential:      wsGroupPricingKey,
		accountRateMultiplier: &accountRate,
		// 3.0 → 0.3：账号倍率 1.0 在新售价下已越线，第二个 turn 必须在转发上游前被拒。
		afterFirstUpstreamRequest: func(*service.ChannelService) error {
			return repo.adminUpdate(apiKeyService, func(_ *service.APIKey, group *service.Group) { group.RateMultiplier = 0.3 })
		},
		secondTurnCloseExpected: true,
		closeStatus:             coderws.StatusTryAgainLater,
		closeReason:             "no longer eligible for this connection",
	})
}

func TestOpenAIResponsesWebSocket_KeyMovedToAnotherGroupRequiresReconnect(t *testing.T) {
	repo := newWSGroupPricingAPIKeyRepoStub(3.0)
	apiKeyService := newWSGroupPricingAPIKeyService(repo)
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            wsGroupPricingFirst,
		secondPayload:           wsGroupPricingSecond,
		apiKeyService:           apiKeyService,
		apiKeyCredential:        wsGroupPricingKey,
		secondTurnCloseExpected: true,
		closeReason:             "connection policy changed",
		afterFirstUpstreamRequest: func(*service.ChannelService) error {
			return repo.adminUpdate(apiKeyService, func(apiKey *service.APIKey, group *service.Group) {
				other := wsGroupPricingGroupID + 1
				apiKey.GroupID = &other
				group.ID = other
				group.RateMultiplier = 0.3
			})
		},
	})
}

func TestOpenAIResponsesWebSocket_FailedKeyRefreshStopsNextTurn(t *testing.T) {
	repo := newWSGroupPricingAPIKeyRepoStub(3.0)
	apiKeyService := newWSGroupPricingAPIKeyService(repo)
	runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
		firstPayload:            wsGroupPricingFirst,
		secondPayload:           wsGroupPricingSecond,
		apiKeyService:           apiKeyService,
		apiKeyCredential:        wsGroupPricingKey,
		secondTurnCloseExpected: true,
		closeStatus:             coderws.StatusTryAgainLater,
		closeReason:             "temporarily unavailable",
		afterFirstUpstreamRequest: func(*service.ChannelService) error {
			return repo.adminUpdate(apiKeyService, func(_ *service.APIKey, group *service.Group) {
				group.RateMultiplier = 0.3
				repo.err = errors.New("auth store unavailable")
			})
		},
	})

	require.GreaterOrEqual(t, repo.lookupCount(), 2)
}

type wsTurnAPIKeyLookupFunc func(ctx context.Context, key string) (*service.APIKey, error)

func (f wsTurnAPIKeyLookupFunc) GetByKey(ctx context.Context, key string) (*service.APIKey, error) {
	return f(ctx, key)
}

func TestRefreshOpenAIWSTurnBillingAPIKey(t *testing.T) {
	groupID := int64(77)
	conn := &service.APIKey{ID: 5, UserID: 9, Key: "sk-conn", GroupID: &groupID,
		User: &service.User{ID: 9}, Group: &service.Group{ID: groupID, Platform: service.PlatformOpenAI, RateMultiplier: 3}}
	latest := *conn
	latest.User = &service.User{ID: 9, Balance: 123, Concurrency: 2}
	group := *conn.Group
	group.RateMultiplier = 0.3
	latest.Group = &group
	got, err := refreshOpenAIWSTurnBillingAPIKey(context.Background(), wsTurnAPIKeyLookupFunc(func(context.Context, string) (*service.APIKey, error) { return &latest, nil }), conn)
	require.NoError(t, err)
	require.Same(t, &latest, got, "permissions, user limits and pricing share one fresh snapshot")
	require.InDelta(t, 3.0, conn.Group.RateMultiplier, 1e-12, "previous turn snapshot remains immutable")
	for _, change := range []string{"key_id", "user_id", "user", "group", "platform", "subscription", "reasoning", "missing_credential", "lookup_error"} {
		t.Run(change, func(t *testing.T) {
			next := latest
			g := *latest.Group
			next.Group = &g
			original := *conn
			var lookupErr error
			switch change {
			case "key_id":
				next.ID++
			case "user_id":
				next.UserID++
			case "user":
				next.User = nil
			case "group":
				other := groupID + 1
				next.GroupID = &other
			case "platform":
				g.Platform = service.PlatformAnthropic
			case "subscription":
				g.SubscriptionType = service.SubscriptionTypeSubscription
			case "reasoning":
				g.MaxReasoningEffort = "low"
			case "missing_credential":
				original.Key = ""
			case "lookup_error":
				lookupErr = errors.New("unavailable")
			}
			got, err := refreshOpenAIWSTurnBillingAPIKey(context.Background(), wsTurnAPIKeyLookupFunc(func(context.Context, string) (*service.APIKey, error) { return &next, lookupErr }), &original)
			require.Error(t, err)
			require.Nil(t, got, "never fall back to stale authorization")
		})
	}
}

func TestOpenAIWSTurnBillingAPIKeysKeepPreviousTurnUntilItIsRecorded(t *testing.T) {
	conn := &service.APIKey{ID: 1}
	turn2 := &service.APIKey{ID: 2}
	turn3 := &service.APIKey{ID: 3}
	var keys openAIWSTurnBillingAPIKeys
	require.Same(t, conn, keys.forTurn(1, conn), "a turn without BeforeTurn bills with the connection snapshot")
	keys.set(2, turn2)
	keys.set(3, turn3) // turn 3 starts before turn 2's usage is submitted
	require.Same(t, turn2, keys.forTurn(2, conn))
	require.Same(t, turn3, keys.forTurn(3, conn))
	keys.set(4, conn)
	require.Same(t, conn, keys.forTurn(2, conn), "only the current and previous turn are kept")
	require.Len(t, keys.keys, 2)
}
