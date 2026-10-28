//go:build unit

package handler

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/config"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

type wsFallbackProfitAccountRepo struct {
	service.AccountRepository
	account *service.Account
	err     error
	calls   int
}

func (r *wsFallbackProfitAccountRepo) GetByID(context.Context, int64) (*service.Account, error) {
	r.calls++
	return r.account, r.err
}

type wsFallbackProfitRelease struct {
	lease  string
	unused bool
}

type wsFallbackProfitTrafficGate struct {
	handlerFallbackTrafficGate
	releaseMu sync.Mutex
	releases  []wsFallbackProfitRelease
}

func (g *wsFallbackProfitTrafficGate) ReleaseGroupQualityTraffic(_ context.Context, lease string, unused bool) error {
	g.releaseMu.Lock()
	defer g.releaseMu.Unlock()
	g.releases = append(g.releases, wsFallbackProfitRelease{lease: lease, unused: unused})
	return nil
}

func TestOpenAIGroupTrafficWSFallbackRechecksProfitAfterTurnSlot(t *testing.T) {
	for _, tc := range []struct {
		name        string
		latestRate  float64
		readErr     error
		normalReady bool
		vetoed      bool
	}{
		{name: "rate raised during fallback admission", latestRate: 0.2, vetoed: true},
		{name: "rate read fails after fallback admission", readErr: errors.New("account storage unavailable"), vetoed: true},
		{name: "affordable fallback remains eligible", latestRate: 0.08},
		{name: "recovered ordinary admission keeps original behavior", normalReady: true, readErr: errors.New("account storage unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			const groupID = int64(3131)
			account := profitSlotTestAccount(1, 0.08)
			repo := &wsFallbackProfitAccountRepo{account: account}
			gateway := service.NewOpenAIGatewayService(repo, nil, nil, nil, nil, nil, nil, &config.Config{}, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil, nil)
			gate := &wsFallbackProfitTrafficGate{handlerFallbackTrafficGate: handlerFallbackTrafficGate{normalReady: tc.normalReady}}
			acquires := 0
			cache := &concurrencyCacheMock{acquireAccountSlotFn: func(ctx context.Context, _ int64, _ int, _ string) (bool, error) {
				acquires++
				_, fallback := service.GroupQualityFallbackFromContext(ctx)
				if fallback || tc.normalReady {
					// The pre-slot check saw 0.08. Only the authoritative read
					// after this admission can observe the changed account.
					repo.account = profitSlotTestAccount(1, tc.latestRate)
					repo.err = tc.readErr
				}
				return true, nil
			}}
			concurrency := service.ProvideConcurrencyService(cache, nil, nil, gate)
			h := &OpenAIGatewayHandler{gatewayService: gateway, concurrencyHelper: NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)}
			group := &service.Group{ID: groupID, Platform: service.PlatformOpenAI, Status: service.StatusActive, Hydrated: true, RateMultiplier: 0.11, ProfitControlEnabled: true, ProfitMinMargin: 0.2}
			ctx := context.WithValue(context.Background(), ctxkey.Group, group)
			ctx, _ = gateway.WithOpenAIRequestPricingContext(ctx, &group.ID)
			ctx = service.GroupQualityTrafficContext(ctx, service.GroupQualityTrafficRequest{Key: "turn-2", GroupID: groupID, Model: "gpt-5.2"})
			ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{GroupID: groupID, AccountID: account.ID, Model: "gpt-5.1"})
			_, vetoed, _ := gateway.ProfitControlVetoLatest(ctx, account)
			require.False(t, vetoed, "the existing check before slot acquisition must allow the original rate")
			require.Equal(t, 1, repo.calls)

			release, acquired, err := h.tryAcquireOpenAIWSTurnAccountSlot(ctx, account.ID, 2, &account)
			if tc.normalReady {
				require.NoError(t, err)
				require.True(t, acquired)
				require.Equal(t, 1, repo.calls, "ordinary admission must not gain an additional database check")
				require.Equal(t, 1, acquires)
				require.False(t, service.GroupQualityFallbackActive(ctx))
			} else {
				require.Equal(t, 2, acquires, "normal quality rejection is followed by one fallback admission")
				require.Equal(t, 2, repo.calls, "fallback must read the account again after acquiring its slot")
			}
			if tc.vetoed {
				require.ErrorIs(t, err, service.ErrAccountAdmissionDenied)
				require.False(t, acquired)
				require.Nil(t, release)
				require.Equal(t, int32(2), atomic.LoadInt32(&cache.releaseAccountCalled), "both rejected normal and fallback slots must be released")
				traffic, ok := service.GroupQualityTrafficRequestFromContext(ctx)
				require.True(t, ok)
				require.Empty(t, traffic.LeaseID, "pre-send profit rejection must clear the unused traffic reservation")
				require.Len(t, gate.releases, 2)
				require.True(t, gate.releases[0].unused, "refund must happen before ordinary lease cleanup")
				require.False(t, gate.releases[1].unused)
				require.NotEmpty(t, gate.releases[0].lease)
				require.Equal(t, gate.releases[0].lease, gate.releases[1].lease)
				return
			}
			require.NoError(t, err)
			require.True(t, acquired)
			require.NotNil(t, release)
			if !tc.normalReady {
				require.Same(t, repo.account, account, "eligible fallback uses the refreshed account snapshot")
			}
			release()
			release()
			require.Equal(t, int32(acquires), atomic.LoadInt32(&cache.releaseAccountCalled))
			require.Len(t, gate.releases, 1)
			require.False(t, gate.releases[0].unused, "an admitted turn must not receive a pre-send refund")
		})
	}
}

func TestOpenAIGroupTrafficWSFallbackRejectsMissingAccount(t *testing.T) {
	for name, account := range map[string]**service.Account{"missing pointer": nil, "missing account": new(*service.Account)} {
		t.Run(name, func(t *testing.T) {
			acquires := 0
			cache := &concurrencyCacheMock{acquireAccountSlotFn: func(context.Context, int64, int, string) (bool, error) {
				acquires++
				return true, nil
			}}
			gate := &wsFallbackProfitTrafficGate{}
			concurrency := service.ProvideConcurrencyService(cache, nil, nil, gate)
			h := &OpenAIGatewayHandler{concurrencyHelper: NewConcurrencyHelper(concurrency, SSEPingFormatNone, 0)}
			ctx := service.GroupQualityTrafficContext(context.Background(), service.GroupQualityTrafficRequest{Key: "turn-2", GroupID: 3131, Model: "gpt-5.2"})
			ctx = service.WithGroupQualityFallback(ctx, service.GroupQualityFallbackRequest{GroupID: 3131, AccountID: 1, Model: "gpt-5.1"})
			release, acquired, err := h.tryAcquireOpenAIWSTurnAccountSlot(ctx, 1, 1, account)
			require.Error(t, err)
			require.False(t, acquired)
			require.Nil(t, release)
			require.Zero(t, acquires, "a missing credential cannot reserve an upstream slot")
			require.Empty(t, gate.releases)
		})
	}
}
