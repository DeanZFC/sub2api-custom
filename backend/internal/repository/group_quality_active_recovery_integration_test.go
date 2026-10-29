//go:build integration

package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestActiveRecoveryStrictCustomerAdmissionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	_, err := db.Exec(`UPDATE passive_quality_policies SET config=jsonb_set(config,'{recovery_mode}','"active"') WHERE id=$1`, p.ID)
	require.NoError(t, err)
	p.RecoveryMode = "active"
	enabled, err := r.GroupQualityActiveRecoveryEnabled(ctx, 8, "A")
	require.NoError(t, err)
	require.True(t, enabled)
	enabled, err = r.GroupQualityActiveRecoveryEnabled(ctx, 9, "A")
	require.NoError(t, err)
	require.False(t, enabled)
	groupTrafficState(t, ctx, db, p, 62, "A", "paused")
	req := trafficRequest("strict-customer", "A", "strict-customer", 65536)
	allowed, reserved := acquireFallback(t, r, req, 62)
	require.False(t, allowed)
	require.False(t, reserved)
	allowed, reserved, err = r.AcquirePassiveQuality(fallbackTrafficContext(req, 62), 62, "A", "strict-customer")
	require.NoError(t, err)
	require.False(t, allowed, "a forged or stale fallback context cannot bypass active recovery")
	require.False(t, reserved)
	allowed, reserved = acquireTraffic(t, r, trafficRequest("ordinary", "A", "ordinary", 65536), 63)
	require.True(t, allowed, "normal supply requires no customer recovery budget")
	require.False(t, reserved)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM group_quality_traffic_leases`).Scan(&count))
	require.Zero(t, count)
	fallback, err := readGroupQualityFallbackSummary(ctx, db, p, time.Now())
	require.NoError(t, err)
	require.False(t, fallback.Enabled)
	traffic, err := readGroupQualityTrafficSummary(ctx, db, p, time.Now(), 0, 0, true)
	require.NoError(t, err)
	require.False(t, traffic.Enabled)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	active, err := groupQualityAccountActiveRecovery(ctx, tx, 62, "A")
	require.NoError(t, err)
	require.True(t, active)
}

func TestActiveRecoveryRevokesExistingFallbackLeaseIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	groupTrafficState(t, ctx, db, p, 62, "A", "paused")
	groupTrafficState(t, ctx, db, p, 63, "A", "paused")
	req := trafficRequest("legacy-stream", "A", "legacy-stream", 4096)
	allowed, reserved := acquireFallback(t, r, req, 62)
	require.True(t, allowed)
	require.True(t, reserved)
	renewed, err := r.RefreshGroupQualityFallback(ctx, req.LeaseID)
	require.NoError(t, err)
	require.True(t, renewed)
	_, err = db.Exec(`UPDATE passive_quality_policies SET config=jsonb_set(config,'{recovery_mode}','"active"') WHERE id=$1`, p.ID)
	require.NoError(t, err)
	renewed, err = r.RefreshGroupQualityFallback(ctx, req.LeaseID)
	require.NoError(t, err)
	require.False(t, renewed, "an existing stream cannot keep renewing after active recovery is enabled")
	allowed, reserved = acquireFallback(t, r, req, 62)
	require.False(t, allowed)
	require.False(t, reserved)
	allowed, reserved, err = r.AcquirePassiveQuality(fallbackTrafficContext(req, 62), 62, "A", req.LeaseID)
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, reserved)
}
