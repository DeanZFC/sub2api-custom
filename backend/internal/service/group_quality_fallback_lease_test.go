package service

import (
	"context"
	"errors"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type fallbackLeaseRefreshStub struct {
	refresh func(context.Context, string) (bool, error)
}

type fallbackAdmissionGateStub struct{ groupTrafficTestGate }

func (g *fallbackAdmissionGateStub) RefreshGroupQualityFallback(context.Context, string) (bool, error) {
	return true, nil
}

func TestGroupQualityFallbackFinalGateSeesExactLeaseAndRefunds(t *testing.T) {
	for _, allow := range []bool{false, true} {
		gate := &fallbackAdmissionGateStub{groupTrafficTestGate: groupTrafficTestGate{allow: true}}
		svc := NewConcurrencyService(nil)
		svc.passiveQualityGate = gate
		ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn", GroupID: 8, Model: "public"})
		ctx = WithGroupQualityFallback(ctx, GroupQualityFallbackRequest{GroupID: 8, AccountID: 62, Model: "public"})
		svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(live context.Context, aid int64) (bool, error) {
			r, ok := GroupQualityTrafficRequestFromContext(live)
			require.True(t, ok)
			require.NotEmpty(t, r.LeaseID, "active quality bypass requires exact durable reservation")
			require.Equal(t, aid, r.AccountID)
			require.EqualValues(t, 8, r.GroupID)
			return allow, nil
		}))
		released := 0
		result, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true, ReleaseFunc: func() { released++ }})
		if allow {
			require.NoError(t, err)
			require.True(t, GroupQualityFallbackActive(ctx))
			result.ReleaseFunc()
			require.True(t, GroupQualityFallbackActive(ctx), "released fallback must not detach an upstream still observing cancellation")
			require.Equal(t, []bool{false}, gate.trafficReleases)
		} else {
			require.ErrorIs(t, err, ErrAccountAdmissionDenied)
			require.False(t, GroupQualityFallbackActive(ctx))
			require.Equal(t, []bool{true}, gate.trafficReleases)
		}
		require.Equal(t, 1, released)
	}
}

func TestGroupQualityFallbackRequiresRenewalAndExactIdentity(t *testing.T) {
	for _, marker := range []GroupQualityFallbackRequest{{GroupID: 8, AccountID: 62, Model: "public"}, {GroupID: 8, AccountID: 62, Model: "wrong"}, {GroupID: 9, AccountID: 62, Model: "public"}} {
		gate := &groupTrafficTestGate{allow: true} // intentionally no renewal capability
		svc := NewConcurrencyService(nil)
		svc.passiveQualityGate = gate
		ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn", GroupID: 8, Model: "public"})
		ctx = WithGroupQualityFallback(ctx, marker)
		result, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true})
		require.ErrorIs(t, err, ErrAccountAdmissionDenied)
		require.False(t, result.Acquired)
		require.Empty(t, gate.acquired)
	}
}

func (g fallbackLeaseRefreshStub) RefreshGroupQualityFallback(ctx context.Context, lease string) (bool, error) {
	return g.refresh(ctx, lease)
}

func TestGroupQualityFallbackLeaseLossCancelsOriginalRequest(t *testing.T) {
	for _, tc := range []struct {
		name  string
		owned bool
		err   error
	}{
		{"lost ownership", false, nil},
		{"storage failure", false, errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: 8})
			seen := make(chan string, 1)
			gate := fallbackLeaseRefreshStub{refresh: func(_ context.Context, lease string) (bool, error) { seen <- lease; return tc.owned, tc.err }}
			stop := startGroupQualityFallbackLease(ctx, gate, "exact-lease", time.Millisecond)
			defer stop()
			select {
			case <-ctx.Done():
			case <-time.After(time.Second):
				t.Fatal("lost emergency lease did not cancel upstream context")
			}
			require.ErrorIs(t, context.Cause(ctx), ErrGroupQualityFallbackLeaseLost)
			require.Equal(t, "exact-lease", <-seen)
		})
	}
}

func TestGroupQualityFallbackLeaseStopWaitsForRefresh(t *testing.T) {
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "request", GroupID: 8})
	entered, resume := make(chan struct{}), make(chan struct{})
	var enteredOnce sync.Once
	gate := fallbackLeaseRefreshStub{refresh: func(context.Context, string) (bool, error) {
		enteredOnce.Do(func() { close(entered) })
		<-resume
		return true, nil
	}}
	stop := startGroupQualityFallbackLease(ctx, gate, "lease", time.Millisecond)
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("refresh did not start")
	}
	stopped := make(chan struct{})
	go func() { stop(); close(stopped) }()
	select {
	case <-stopped:
		t.Fatal("released ownership while refresh still running")
	default:
	}
	close(resume)
	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("stop did not finish")
	}
	stop() // idempotent release
	require.NoError(t, ctx.Err(), "normal lease release must not cancel the response")
}

func TestGroupQualityFallbackCancellationSurvivesWSTurnAndUsageFreeze(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("GET", "/v1/responses", nil)
	beginGroupQualityTrafficTurn(c)
	original := c.Request.Context()
	SetGroupQualityTrafficGroup(c, 8)
	bindGroupQualityTrafficLease(original, nil, 7, "public", "lease")
	bindGroupQualityFallbackStop(original, "lease", func() {})
	require.True(t, GroupQualityFallbackActive(original))
	frozen := freezeGroupQualityTrafficContext(original, context.Background())
	beginGroupQualityTrafficTurn(c)
	require.False(t, GroupQualityFallbackActive(c.Request.Context()), "new turn must not inherit emergency status")
	cancelGroupQualityTraffic(c.Request.Context(), ErrGroupQualityFallbackLeaseLost)
	require.ErrorIs(t, context.Cause(original), ErrGroupQualityFallbackLeaseLost)
	require.NoError(t, frozen.Err(), "async usage still settles its immutable prior lease")
	request, ok := GroupQualityTrafficRequestFromContext(frozen)
	require.True(t, ok)
	require.Equal(t, "lease", request.LeaseID)
}
