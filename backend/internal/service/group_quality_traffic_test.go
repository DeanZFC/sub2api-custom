package service

import (
	"context"
	"errors"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
	"net/http/httptest"
	"testing"
)

type qualityAdmissionTestGate struct {
	PassiveQualityRepository
	allow    bool
	err      error
	reserved bool
	releases []bool
	acquired []string
}

func (g *qualityAdmissionTestGate) AcquirePassiveQuality(_ context.Context, _ int64, model, _ string) (bool, bool, error) {
	g.acquired = append(g.acquired, model)
	return g.allow, g.reserved, g.err
}
func (g *qualityAdmissionTestGate) ReleasePassiveQuality(_ context.Context, _ string, unused bool) error {
	g.releases = append(g.releases, unused)
	return nil
}
func TestQualityAdmissionLifecycle(t *testing.T) {
	unavailable := errors.New("quality repository unavailable")
	for _, tt := range []struct {
		name                          string
		allow, reserved, activeDenied bool
		err, wantErr                  error
	}{
		{name: "accepted", allow: true, reserved: true},
		{name: "passive held", wantErr: ErrAccountQualityHeld},
		{name: "active hard denial", allow: true, reserved: true, activeDenied: true, wantErr: ErrAccountAdmissionDenied},
		{name: "repository unavailable", err: unavailable, wantErr: unavailable},
	} {
		t.Run(tt.name, func(t *testing.T) {
			gate := &qualityAdmissionTestGate{allow: tt.allow, reserved: tt.reserved, err: tt.err}
			service := NewConcurrencyService(nil)
			service.passiveQualityGate = gate
			if tt.activeDenied {
				service.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) { return false, nil }))
			}
			ctx := GroupQualityTrafficContext(context.WithValue(context.Background(), ctxkey.Model, "provider"), GroupQualityTrafficRequest{Key: "turn", GroupID: 8, Model: "public"})
			released := 0
			result, err := service.admitCacheRecoveryRequest(ctx, 62, &AcquireResult{Acquired: true, ReleaseFunc: func() { released++ }})
			require.Equal(t, []string{"public"}, gate.acquired)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
				require.False(t, result.Acquired)
				require.Equal(t, 1, released)
				if tt.reserved || tt.err != nil {
					require.Equal(t, []bool{true}, gate.releases)
				} else {
					require.Empty(t, gate.releases)
				}
				return
			}
			require.NoError(t, err)
			require.True(t, result.Acquired)
			result.ReleaseFunc()
			result.ReleaseFunc()
			require.Equal(t, 1, released)
			require.Equal(t, []bool{false}, gate.releases)
		})
	}
}
func TestQualityAdmissionBusySlotDoesNotReserve(t *testing.T) {
	gate := &qualityAdmissionTestGate{allow: true}
	service := NewConcurrencyService(nil)
	service.passiveQualityGate = gate
	result, err := service.admitCacheRecoveryRequest(context.Background(), 62, &AcquireResult{})
	require.NoError(t, err)
	require.False(t, result.Acquired)
	require.Empty(t, gate.acquired)
}
func TestQualityAdmissionProxyValidationReleasesConcurrency(t *testing.T) {
	gate := &qualityAdmissionTestGate{allow: true, reserved: true}
	service := NewConcurrencyService(nil)
	service.passiveQualityGate = gate
	released := 0
	result, err := service.admitCacheRecoveryRequest(context.Background(), 62, &AcquireResult{Acquired: true, ProxyID: 99, ReleaseFunc: func() { released++ }})
	require.NoError(t, err)
	_, err = result.RouteAccount(&Account{ID: 62})
	require.Error(t, err)
	require.Equal(t, 1, released)
	require.Equal(t, []bool{false}, gate.releases)
}
func TestGroupQualityEvidenceTurnSnapshot(t *testing.T) {
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest("POST", "/v1/responses", nil)
	PassiveQualityBeginTurn(c)
	SetGroupQualityTrafficGroup(c, 8)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte(`{"model":"public","previous_response_id":"resp_1"}`))
	first, ok := GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.True(t, ok)
	frozen := freezeGroupQualityTrafficContext(c.Request.Context(), context.Background())
	PassiveQualityBeginTurn(c)
	PrepareGroupQualityTrafficBody(c.Request.Context(), []byte(`{"model":"next","input":"hello"}`))
	second, ok := GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.True(t, ok)
	require.NotEqual(t, first.Key, second.Key)
	require.EqualValues(t, 8, second.GroupID)
	require.Equal(t, "next", second.Model)
	snapshot, ok := GroupQualityTrafficRequestFromContext(frozen)
	require.True(t, ok)
	require.Equal(t, first, snapshot)
}
func TestGroupQualityEvidenceExcludesMediaAndResetsCompaction(t *testing.T) {
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
	ExcludeGroupQualityTraffic(c.Request.Context())
	_, ok = GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.False(t, ok)
	c.Request = httptest.NewRequest("POST", "/v1/responses/compact", nil)
	PassiveQualityBeginTurn(c)
	SetGroupQualityTrafficGroup(c, 8)
	_, ok = GroupQualityTrafficRequestFromContext(c.Request.Context())
	require.False(t, ok)
}

func TestQualityAdmissionFinalGatePreservesReason(t *testing.T) {
	unavailable := errors.New("final account database unavailable")
	for _, cause := range []error{ErrAccountQualityHeld, ErrAccountAdmissionDenied, unavailable} {
		t.Run(cause.Error(), func(t *testing.T) {
			service := NewConcurrencyService(nil)
			gate := &qualityAdmissionTestGate{allow: true, reserved: true}
			service.passiveQualityGate = gate
			service.SetCacheRecoveryGate(cacheRecoveryGateFunc(func(context.Context, int64) (bool, error) { return false, cause }))
			released := 0
			result, err := service.admitCacheRecoveryRequest(context.Background(), 62, &AcquireResult{Acquired: true, ReleaseFunc: func() { released++ }})
			require.ErrorIs(t, err, cause)
			require.False(t, result.Acquired)
			require.Equal(t, 1, released)
			require.Equal(t, []bool{true}, gate.releases)
			if cause != ErrAccountQualityHeld {
				require.NotErrorIs(t, err, ErrAccountQualityHeld)
			}
		})
	}
}
