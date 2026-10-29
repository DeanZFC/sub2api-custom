package service

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type activeRecoveryFallbackGate struct {
	groupQualityFallbackTestGate
	active bool
	err    error
}

func (g *activeRecoveryFallbackGate) GroupQualityActiveRecoveryEnabled(context.Context, int64, string) (bool, error) {
	return g.active, g.err
}

func TestActiveRecoveryRejectsEmergencyCustomerTraffic(t *testing.T) {
	for _, mode := range []string{"legacy", "legacy_nonbatch", "advanced"} {
		t.Run(mode, func(t *testing.T) {
			resetOpenAIAdvancedSchedulerSettingCacheForTest()
			defer resetOpenAIAdvancedSchedulerSettingCacheForTest()
			gid := int64(43)
			gate := &activeRecoveryFallbackGate{groupQualityFallbackTestGate: groupQualityFallbackTestGate{recovery: true}, active: true}
			svc := groupQualityFallbackService(mode, []Account{groupQualityFallbackTestAccount(1, gid, 0, true)}, &gate.groupQualityFallbackTestGate)
			svc.concurrencyService.passiveQualityGate = gate
			ctx, req := groupQualityAffinityRequest(&gid, "public-model", "")
			selection, err := svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.ErrorIs(t, err, ErrNoAvailableAccounts)
			require.Nil(t, selection)
			require.Empty(t, gate.calls, "no customer lease may be acquired for an active recovery target")
			gate.err = errors.New("policy read failed")
			_, err = svc.selectGroupQualityFallback(ctx, req, ErrNoAvailableAccounts)
			require.ErrorIs(t, err, gate.err, "a policy lookup error must not silently reopen suspended supply")
		})
	}
}

func TestActiveRecoveryProjectionKeepsOwnerSuspended(t *testing.T) {
	gid := int64(43)
	a := groupQualityFallbackTestAccount(1, gid, 0, false)
	holds, ok := a.Extra["passive_quality"].([]any)
	require.True(t, ok)
	require.NotEmpty(t, holds)
	hold, ok := holds[0].(map[string]any)
	require.True(t, ok)
	hold["recovery_mode"] = "active"
	ctx := WithGroupQualityFallback(context.Background(), GroupQualityFallbackRequest{GroupID: gid, AccountID: a.ID, Model: "public-model"})
	adjusted := groupQualityFallbackAccount(ctx, &a, "public-model")
	require.Same(t, &a, adjusted)
	require.True(t, adjusted.PassiveQualityBlocked("public-model"))
	require.False(t, groupQualityRequiresActiveRecovery(&a, "other-model"))
}
