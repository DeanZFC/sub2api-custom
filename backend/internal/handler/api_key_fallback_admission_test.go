//go:build unit

package handler

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/server/middleware"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type auditFallbackAccountRepo struct{ strictRequestAccountRepo }

func (r auditFallbackAccountRepo) ListSchedulableByGroupIDAndPlatform(_ context.Context, id int64, platform string) ([]service.Account, error) {
	rows := []service.Account{}
	for _, a := range r.accounts {
		for _, g := range a.GroupIDs {
			if g == id && a.Platform == platform {
				rows = append(rows, a)
			}
		}
	}
	return rows, nil
}

type auditFallbackBillingCache struct {
	service.BillingCache
	balanceReads       int
	subscriptionGroups []int64
}

func (c *auditFallbackBillingCache) GetUserBalance(context.Context, int64) (float64, error) {
	c.balanceReads++
	return 0, nil
}
func (c *auditFallbackBillingCache) GetSubscriptionCache(_ context.Context, _, g int64) (*service.SubscriptionCacheData, error) {
	c.subscriptionGroups = append(c.subscriptionGroups, g)
	return &service.SubscriptionCacheData{Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)}, nil
}
func TestAPIKeyFallbackRejectsZeroBalanceBeforeUpstream(t *testing.T) {
	primaryID, fallbackID := int64(3131), int64(3132)
	primary := &service.Group{ID: primaryID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeSubscription, UserConcurrencyLimit: 5}
	fallback := &service.Group{ID: fallbackID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, SubscriptionType: service.SubscriptionTypeStandard, UserConcurrencyLimit: 1}
	repo := auditFallbackAccountRepo{strictRequestAccountRepo{openAIImagesFailoverAccountRepo{accounts: []service.Account{{ID: 6, Name: "fallback", Platform: service.PlatformOpenAI, Type: service.AccountTypeOAuth, Status: service.StatusActive, Schedulable: true, GroupIDs: []int64{fallbackID}, Credentials: map[string]any{"access_token": "test-token"}}}}}}
	upstream := &strictRequestUpstream{winner: 6}
	h := newOpenAIResponsesFailoverTestHandler(t, upstream, repo)
	h.cfg.RunMode = "standard"
	cache := &helperConcurrencyCacheStub{userGroupSeq: []bool{true}}
	h.concurrencyHelper = NewConcurrencyHelper(service.NewConcurrencyService(cache), SSEPingFormatNone, time.Millisecond)
	billingCache := &auditFallbackBillingCache{}
	h.billingCacheService = service.NewBillingCacheService(billingCache, nil, nil, nil, nil, nil, &config.Config{}, nil)
	t.Cleanup(h.billingCacheService.Stop)
	c, rec := newOpenAIResponsesFailoverTestContext(t, nil)
	key := c.MustGet(string(middleware.ContextKeyAPIKey)).(*service.APIKey)
	key.Group = primary
	key.FallbackGroupID = &fallbackID
	key.FallbackGroup = fallback
	key.User.Balance = 0
	c.Set(string(middleware.ContextKeySubscription), &service.UserSubscription{ID: 101, UserID: key.User.ID, GroupID: primaryID, Status: service.SubscriptionStatusActive, ExpiresAt: time.Now().Add(time.Hour)})
	c.Set(string(middleware.ContextKeyUser), middleware.AuthSubject{UserID: key.User.ID, Concurrency: 10})
	ctx := context.WithValue(c.Request.Context(), ctxkey.Group, primary)
	c.Request = c.Request.WithContext(service.WithAPIKeyGroupFallbackRouting(ctx, key))
	h.Responses(c)
	require.Equal(t, http.StatusForbidden, rec.Code, rec.Body.String())
	require.Empty(t, upstream.calls())
	require.Equal(t, primaryID, *key.GroupID)
	require.Equal(t, 1, billingCache.balanceReads)
	require.Equal(t, 1, cache.userGroupAcquireCalls)
}
