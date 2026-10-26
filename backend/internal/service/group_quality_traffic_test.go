package service

import (
	"context"
	"math"
	"net/http/httptest"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityTrafficBudget(t *testing.T) {
	for _, tt := range []struct {
		name        string
		input, read int64
		target      float64
		verified    int
		mode        string
		budget      int64
	}{
		{"bootstrap", 0, 0, 80, 0, "bootstrap", 1_000_000},
		{"below target", 1_000_000, 700_000, 80, 1, "conserving", 10_000},
		{"no headroom", 1_000_000, 800_000, 80, 1, "conserving", 10_000},
		{"floor", 0, 0, 80, 1, "conserving", 4096},
		{"headroom", 1_000_000, 840_000, 80, 1, "normal", 25_000},
		{"five percent cap", 1_000_000, 990_000, 80, 1, "normal", 50_000},
		{"invalid target", 1_000_000, 990_000, math.NaN(), 1, "conserving", 0},
		{"disabled target", 1_000_000, 990_000, 0, 1, "conserving", 0},
	} {
		t.Run(tt.name, func(t *testing.T) {
			mode, budget := GroupQualityTrafficBudget(tt.input, tt.read, tt.target, tt.verified)
			require.Equal(t, tt.mode, mode)
			require.Equal(t, tt.budget, budget)
		})
	}
}

type groupTrafficTestGate struct {
	PassiveQualityRepository
	allow                            bool
	passiveReleases, trafficReleases []bool
	acquired                         []string
	settled                          []GroupQualityTrafficRequest
	reported                         []bool
	settleInputs                     []int64
}

func (g *groupTrafficTestGate) AcquirePassiveQuality(context.Context, int64, string, string) (bool, bool, error) {
	return true, true, nil
}
func (g *groupTrafficTestGate) ReleasePassiveQuality(_ context.Context, _ string, unused bool) error {
	g.passiveReleases = append(g.passiveReleases, unused)
	return nil
}
func (g *groupTrafficTestGate) AcquireGroupQualityTraffic(_ context.Context, _ int64, model, lease string) (bool, bool, error) {
	g.acquired = append(g.acquired, model)
	return g.allow, g.allow, nil
}
func (g *groupTrafficTestGate) ReleaseGroupQualityTraffic(_ context.Context, _ string, unused bool) error {
	g.trafficReleases = append(g.trafficReleases, unused)
	return nil
}
func (g *groupTrafficTestGate) SettleGroupQualityTraffic(_ context.Context, r GroupQualityTrafficRequest, _ int64, input, _ int64, reported bool) error {
	g.settled = append(g.settled, r)
	g.reported = append(g.reported, reported)
	g.settleInputs = append(g.settleInputs, input)
	return nil
}

func TestGroupQualityTrafficAdmissionLifecycle(t *testing.T) {
	for _, tt := range []struct {
		name                string
		allow, activeDenied bool
		wantErr             error
	}{
		{"accepted", true, false, nil},
		{"budget denied", false, false, ErrGroupQualityTrafficBudget},
		{"active denial refunds", true, true, ErrAccountAdmissionDenied},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gate := &groupTrafficTestGate{allow: tt.allow}
			svc := NewConcurrencyService(nil)
			svc.passiveQualityGate = gate
			if tt.activeDenied {
				svc.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) { return false, nil }))
			}
			ctx := GroupQualityTrafficContext(context.WithValue(context.Background(), ctxkey.Model, "mapped-model"), GroupQualityTrafficRequest{Key: "turn", GroupID: 8, Model: "public-model", EstimatedInput: 8192})
			released := 0
			result, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true, ReleaseFunc: func() { released++ }})
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.False(t, result.Acquired)
				require.Equal(t, []bool{true}, gate.passiveReleases)
				if tt.allow {
					require.Equal(t, []bool{true}, gate.trafficReleases)
				} else {
					require.Empty(t, gate.trafficReleases)
				}
				require.Equal(t, 1, released)
				return
			}
			require.NoError(t, err)
			require.Equal(t, []string{"public-model"}, gate.acquired)
			r, ok := GroupQualityTrafficRequestFromContext(ctx)
			require.True(t, ok)
			require.NotEmpty(t, r.LeaseID)
			require.EqualValues(t, 62, r.AccountID)
			result.ReleaseFunc()
			result.ReleaseFunc()
			require.Equal(t, 1, released)
			require.Equal(t, []bool{false}, gate.trafficReleases)
			recordGroupQualityTrafficUsage(ctx, 62, 0, 0, false)
			require.Equal(t, []bool{false}, gate.reported)
			require.Equal(t, []bool{false}, gate.trafficReleases, "missing usage must not refund admission")
		})
	}
}

func TestGroupQualityTrafficBusySlotConsumesNoBudget(t *testing.T) {
	gate := &groupTrafficTestGate{allow: true}
	svc := NewConcurrencyService(nil)
	svc.passiveQualityGate = gate
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "r", GroupID: 8})
	result, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{})
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Empty(t, gate.acquired)
}

func TestGroupQualityTrafficProxyValidationRefundsBeforeRelease(t *testing.T) {
	gate := &groupTrafficTestGate{allow: true}
	svc := NewConcurrencyService(nil)
	svc.passiveQualityGate = gate
	ctx := GroupQualityTrafficContext(context.Background(), GroupQualityTrafficRequest{Key: "turn", GroupID: 8, Model: "public", EstimatedInput: 8192})
	result, err := svc.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true, ProxyID: 99})
	require.NoError(t, err)
	_, err = result.RouteAccount(&Account{ID: 62})
	require.Error(t, err)
	require.Equal(t, []bool{true, false}, gate.trafficReleases, "pre-send refund must precede normal slot release")
}

func TestGroupQualityTrafficTurnFreezeAndUnusedRefund(t *testing.T) {
	gin.SetMode(gin.TestMode)
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	PassiveQualityBeginTurn(c)
	SetGroupQualityTrafficGroup(c, 8)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte(`{"model":"public","previous_response_id":"resp_1"}`))
	first, _ := GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.EqualValues(t, 262144, first.EstimatedInput)
	gate := &groupTrafficTestGate{allow: true}
	bindGroupQualityTrafficLease(c.Request.Context(), gate, 62, "public", "lease-one")
	frozen := freezeGroupQualityTrafficContext(c.Request.Context(), context.Background())
	PassiveQualityBeginTurn(c)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte(`{"model":"next","input":"hello"}`))
	bindGroupQualityTrafficLease(c.Request.Context(), gate, 63, "next", "lease-two")
	second, _ := GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.NotEqual(t, first.Key, second.Key)
	require.EqualValues(t, 8, second.GroupID)
	require.EqualValues(t, 4096, second.EstimatedInput)
	recordGroupQualityTrafficUsage(frozen, 63, 1000, 800, true)
	require.Empty(t, gate.settled)
	recordGroupQualityTrafficUsage(frozen, 62, 1000, 800, true)
	require.Len(t, gate.settled, 1)
	require.Equal(t, "lease-one", gate.settled[0].LeaseID)
	require.Equal(t, "public", gate.settled[0].Model)
	ReleaseUnusedGroupQualityTraffic(c.Request.Context(), 62)
	require.Empty(t, gate.trafficReleases)
	ReleaseUnusedGroupQualityTraffic(c.Request.Context(), 63)
	ReleaseUnusedGroupQualityTraffic(c.Request.Context(), 63)
	require.Equal(t, []bool{true}, gate.trafficReleases)
	require.Equal(t, "lease-one", gate.settled[0].LeaseID)
}

func TestGroupQualityTrafficExcludesNonTextAndResetsCompaction(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	PassiveQualityBeginTurn(c)
	SetGroupQualityTrafficGroup(c, 8)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte(`{"compaction_trigger":{},"model":"m"}`))
	_, ok := GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.False(t, ok)
	PassiveQualityBeginTurn(c)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte(`{"input":"next"}`))
	_, ok = GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.True(t, ok)
	c.Request = httptest.NewRequest("POST", "/v1/responses/compact", nil)
	PassiveQualityBeginTurn(c)
	SetGroupQualityTrafficGroup(c, 8)
	_, ok = GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.False(t, ok)
}
