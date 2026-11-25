package handler

import (
	"context"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/service"
	coderws "github.com/coder/websocket"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type wsPerformanceAccountRepo struct {
	*openAIWSUsageHandlerAccountRepoStub
	admit func(context.Context, int64) (bool, error)
}

func (*wsPerformanceAccountRepo) PerformancePaused(context.Context, []int64) (map[int64]bool, error) {
	return nil, nil
}

func (r *wsPerformanceAccountRepo) AdmitPerformanceRequest(ctx context.Context, id int64) (bool, error) {
	return r.admit(ctx, id)
}

func TestResponsesWebSocketPerformanceAdmissionEachTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		t.Run(mode, func(t *testing.T) {
			var attempts atomic.Int32
			got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
				firstPayload: wsGroupPricingFirst, secondPayload: wsGroupPricingSecond,
				ingressMode: mode,
				admitPerformanceRequest: func(context.Context, int64) (bool, error) {
					return attempts.Add(1) == 1, nil
				},
				secondTurnCloseExpected: true, closeStatus: coderws.StatusTryAgainLater,
				closeReason: "account is no longer eligible", checkFirstTurnUsage: true,
			})
			require.Len(t, got.logs, 1)
			require.EqualValues(t, 2, attempts.Load())
		})
	}
}

func TestOpenAIWSTurnRevalidatesFallbackGroup(t *testing.T) {
	repo := newWSGroupPricingAPIKeyRepoStub(1)
	fallback := repo.group
	fallback.ID++
	repo.apiKey.FallbackGroupID = &fallback.ID
	repo.apiKey.FallbackGroup = &fallback
	keys := newWSGroupPricingAPIKeyService(repo)
	ctx := context.Background()
	primary, err := keys.GetByKey(ctx, wsGroupPricingKey)
	require.NoError(t, err)
	require.NotNil(t, primary.FallbackGroup)
	bound := *primary
	bound.GroupID, bound.Group = primary.FallbackGroupID, primary.FallbackGroup
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	h := &OpenAIGatewayHandler{apiKeyService: keys, cfg: &config.Config{RunMode: config.RunModeSimple}}
	require.NoError(t, repo.adminUpdate(keys, func(key *service.APIKey, _ *service.Group) {
		updated := *key.FallbackGroup
		updated.RateMultiplier = 2
		key.FallbackGroup = &updated
	}))
	fresh, err := h.authorizeOpenAIWSTurn(ctx, c, primary, &bound, nil)
	require.NoError(t, err)
	require.Equal(t, fallback.ID, fresh.Group.ID)
	require.Equal(t, 2.0, fresh.Group.RateMultiplier)
	require.NoError(t, repo.adminUpdate(keys, func(key *service.APIKey, _ *service.Group) {
		key.FallbackGroupID, key.FallbackGroup = nil, nil
	}))
	_, err = h.authorizeOpenAIWSTurn(ctx, c, primary, &bound, nil)
	require.ErrorContains(t, err, "no longer authorized")
}

func TestSmartWSNativeRevocationStopsNextTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		for _, kind := range []string{"key_disabled", "user_disabled", "group_disabled", "exclusive_revoked", "ip_acl", "model_allowlist"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				repo := newWSGroupPricingAPIKeyRepoStub(1)
				keys := newWSGroupPricingAPIKeyService(repo)
				reason := "no longer authorized"
				if kind == "model_allowlist" {
					reason = "not available for this group"
				}
				runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload: wsGroupPricingFirst, secondPayload: wsGroupPricingSecond,
					ingressMode: mode, apiKeyService: keys, apiKeyCredential: wsGroupPricingKey,
					afterFirstUpstreamRequest: func(*service.ChannelService) error {
						return repo.adminUpdate(keys, func(key *service.APIKey, group *service.Group) {
							switch kind {
							case "key_disabled":
								key.Status = service.StatusAPIKeyDisabled
							case "user_disabled":
								key.User.Status = "disabled"
							case "group_disabled":
								group.Status = "disabled"
							case "exclusive_revoked":
								group.IsExclusive = true
								key.User.AllowedGroups = nil
							case "ip_acl":
								key.IPWhitelist = []string{"192.0.2.1"}
							case "model_allowlist":
								group.ModelAllowlist = service.GroupModelAllowlist{Enabled: true, Models: []string{"gpt-4.1"}}
							}
						})
					},
					secondTurnCloseExpected: true, closeReason: reason,
				})
			})
		}
	}
}

type wsAdmissionCache struct {
	service.BillingCache
	blocked atomic.Bool
	kind    string
}

func (s *wsAdmissionCache) GetUserBalance(context.Context, int64) (float64, error) {
	if s.kind == "balance" && s.blocked.Load() {
		return 0, nil
	}
	return 100, nil
}
func (*wsAdmissionCache) DeductUserBalance(context.Context, int64, float64) error { return nil }
func (*wsAdmissionCache) UpdateSubscriptionUsage(context.Context, int64, int64, float64) error {
	return nil
}
func (*wsAdmissionCache) UpdateAPIKeyRateLimitUsage(context.Context, int64, float64) error {
	return nil
}
func (*wsAdmissionCache) InvalidateAPIKeyRateLimit(context.Context, int64) error { return nil }
func (s *wsAdmissionCache) GetSubscriptionCache(context.Context, int64, int64) (*service.SubscriptionCacheData, error) {
	d := &service.SubscriptionCacheData{Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}
	if s.blocked.Load() {
		switch s.kind {
		case "subscription_revoked":
			d.Status = "disabled"
		case "subscription_expired":
			d.ExpiresAt = time.Now().Add(-time.Minute)
		case "subscription_quota":
			d.DailyUsage = 2
		}
	}
	return d, nil
}
func (s *wsAdmissionCache) GetAPIKeyRateLimit(context.Context, int64) (*service.APIKeyRateLimitCacheData, error) {
	return &service.APIKeyRateLimitCacheData{Usage5h: 2, Window5h: time.Now().Unix()}, nil
}

type wsAdmissionRPM struct {
	service.UserRPMCache
	user  atomic.Int64
	group atomic.Int64
}

// The admission fixture records usage without touching a real wallet.
type wsAdmissionUsageBilling struct{ service.UsageBillingRepository }

func (*wsAdmissionUsageBilling) Apply(context.Context, *service.UsageBillingCommand) (*service.UsageBillingApplyResult, error) {
	return &service.UsageBillingApplyResult{}, nil
}

type wsAdmissionSubscription struct {
	sub      *service.UserSubscription
	changed  *atomic.Bool
	replaced bool
}

func (s *wsAdmissionSubscription) GetActiveSubscription(context.Context, int64, int64) (*service.UserSubscription, error) {
	current := *s.sub
	if s.replaced && s.changed.Load() {
		current.ID++
	}
	return &current, nil
}
func (*wsAdmissionSubscription) ValidateAndCheckLimits(*service.UserSubscription, *service.Group) (bool, error) {
	return false, nil
}
func (*wsAdmissionSubscription) EnsureWindowMaintenance(_ context.Context, s *service.UserSubscription) (*service.UserSubscription, error) {
	return s, nil
}

func (s *wsAdmissionRPM) IncrementUserRPM(context.Context, int64) (int, error) {
	return int(s.user.Add(1)), nil
}
func (s *wsAdmissionRPM) IncrementUserGroupRPM(context.Context, int64, int64) (int, error) {
	return int(s.group.Add(1)), nil
}

func TestSmartWSNativeStandardBillingEachTurn(t *testing.T) {
	for _, mode := range []string{service.OpenAIWSIngressModeCtxPool, service.OpenAIWSIngressModePassthrough} {
		for _, kind := range []string{"key_expired", "key_quota", "key_window", "balance", "auth_balance", "user_rpm", "group_rpm", "subscription_revoked", "subscription_expired", "subscription_quota", "subscription_replaced", "rpm_once_per_turn"} {
			t.Run(mode+"/"+kind, func(t *testing.T) {
				repo := newWSGroupPricingAPIKeyRepoStub(1)
				repo.apiKey.User.Balance = 100
				cache := &wsAdmissionCache{kind: kind}
				rpm := &wsAdmissionRPM{}
				repo.apiKey.User.RPMLimit = 2
				if kind == "user_rpm" {
					repo.apiKey.User.RPMLimit = 1
				}
				if kind == "group_rpm" {
					repo.group.RPMLimit = 1
				}
				var subscription *service.UserSubscription
				if kind == "subscription_revoked" || kind == "subscription_expired" || kind == "subscription_quota" || kind == "subscription_replaced" {
					repo.group.SubscriptionType = service.SubscriptionTypeSubscription
					limit := 1.0
					repo.group.DailyLimitUSD = &limit
					subscription = &service.UserSubscription{ID: 1, UserID: repo.apiKey.UserID, GroupID: repo.group.ID, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}
				}
				keys := newWSGroupPricingAPIKeyService(repo)
				var slots atomic.Int64
				got := runOpenAIResponsesWebSocketUsageLogCase(t, openAIResponsesWSUsageLogCase{
					firstPayload: wsGroupPricingFirst, secondPayload: wsGroupPricingSecond,
					ingressMode: mode, apiKeyService: keys, apiKeyCredential: wsGroupPricingKey,
					subscription:       subscription,
					subscriptionLookup: &wsAdmissionSubscription{sub: subscription, changed: &cache.blocked, replaced: kind == "subscription_replaced"},
					configureBilling: func(cfg *config.Config) *service.BillingCacheService {
						cfg.RunMode = config.RunModeStandard
						return service.NewBillingCacheService(cache, nil, nil, nil, rpm, nil, cfg, nil)
					},
					checkUserConcurrency: func(limit int) {
						if slots.Add(1) > 1 {
							require.Equal(t, 2, limit, "next turn uses the current user concurrency")
						}
					},
					afterFirstUpstreamRequest: func(*service.ChannelService) error {
						cache.blocked.Store(true)
						return repo.adminUpdate(keys, func(key *service.APIKey, _ *service.Group) {
							key.User.Concurrency = 2
							switch kind {
							case "auth_balance":
								key.User.Balance = 0
							case "key_expired":
								at := time.Now().Add(-time.Minute)
								key.ExpiresAt = &at
							case "key_quota":
								key.Quota, key.QuotaUsed = 1, 1
							case "key_window":
								key.RateLimit5h = 1
							}
						})
					},
					secondTurnCloseExpected: kind != "rpm_once_per_turn", closeReason: "billing check failed",
				})
				if kind == "rpm_once_per_turn" {
					require.Len(t, got.logs, 2)
					require.EqualValues(t, 2, rpm.user.Load(), "neither skip a turn nor double-count the first")
				}
			})
		}
	}
}
