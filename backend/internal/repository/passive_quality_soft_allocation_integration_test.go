//go:build integration

package repository

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupTargetRetiredSoftAllocationNeverProjectsRoutingLimitsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, Purpose: p.Purpose, AccountID: 62, Model: "A", Scope: p.Scope, Mode: p.Mode, Version: p.Version, Phase: "suspect", Hits: 1, ConfirmationMetric: "cache_rate", UpdatedAt: time.Now()}
	project := func() service.Account {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		require.NoError(t, savePassiveState(ctx, tx, s))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
		require.NoError(t, tx.Commit())
		var a service.Account
		var raw []byte
		require.NoError(t, db.QueryRow(`SELECT status,schedulable,extra FROM accounts WHERE id=62`).Scan(&a.Status, &a.Schedulable, &raw))
		require.NoError(t, json.Unmarshal(raw, &a.Extra))
		require.Equal(t, "active", a.Status)
		require.True(t, a.Schedulable)
		require.False(t, a.PassiveQualityBlocked("A"), "suspect performance must preserve existing owners")
		require.Equal(t, 100, a.PassiveQualityNewSessionPercent("B"), "another model must not be reduced")
		return a
	}
	for _, metric := range []string{"cache_rate", "ttft_p90", "output_tps"} {
		s.ConfirmationMetric = metric
		a := project()
		require.Equal(t, 100, a.PassiveQualityNewSessionPercent("A"))
	}
	s.Hits = 0
	a := project()
	require.Equal(t, 100, a.PassiveQualityNewSessionPercent("A"), "cleared performance suspicion removes its temporary allocation restriction")
	s.Hits, s.ConfirmationMetric = 1, "error_rate"
	a = project()
	require.Equal(t, 100, a.PassiveQualityNewSessionPercent("A"), "availability uses its separate confirmation gate")
	// Cache hysteresis is independent of the currently failing hard metric.
	s.CacheAllocationReduced, s.Hits, s.ConfirmationMetric = true, 0, "error_rate"
	a = project()
	require.Equal(t, 100, a.PassiveQualityNewSessionPercent("A"), "retired cache allocation latch cannot reduce new customer traffic")
	s.Phase = "insufficient"
	a = project()
	require.Equal(t, 100, a.PassiveQualityNewSessionPercent("A"), "missing evidence cannot recreate retired allocation restrictions")
	s.Phase, s.Hits = "healthy", 0
	a = project()
	require.Equal(t, 100, a.PassiveQualityNewSessionPercent("A"))
	require.NotContains(t, a.Extra, "passive_quality")
}
