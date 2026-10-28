//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func trafficRequest(key, model, lease string, estimate int64) service.GroupQualityTrafficRequest {
	return service.GroupQualityTrafficRequest{Key: key, GroupID: 8, Model: model, LeaseID: lease, EstimatedInput: estimate}
}
func acquireTraffic(t *testing.T, r *passiveQualityRepository, req service.GroupQualityTrafficRequest, aid int64) (bool, bool) {
	t.Helper()
	allowed, reserved, err := r.AcquireGroupQualityTraffic(service.GroupQualityTrafficContext(context.Background(), req), aid, req.Model, req.LeaseID)
	require.NoError(t, err)
	return allowed, reserved
}
func healthyTrafficState(t *testing.T, ctx context.Context, db *sql.DB, r *passiveQualityRepository, p service.PassiveQualityPolicy, model string) {
	t.Helper()
	for i := 0; i < 5; i++ {
		insertGroupTargetUsage(t, db, r, fmt.Sprintf("healthy-%s-%d", model, i), model, 8, time.Now(), 90, true)
	}
	_, err := r.advance(ctx, p.ID, 62, time.Now(), false, model)
	require.NoError(t, err)
	require.Equal(t, "healthy", targetStates(t, ctx, r)[model].Phase)
}

func TestGroupQualityTrafficAtomicGroupBudgetIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			req := trafficRequest(fmt.Sprint(i), fmt.Sprintf("model-%d", i%3), fmt.Sprintf("lease-%d", i), 250000)
			other := &passiveQualityRepository{db: db}
			allowed, reserved, err := other.AcquireGroupQualityTraffic(service.GroupQualityTrafficContext(ctx, req), 62+int64(i%2), req.Model, req.LeaseID)
			if err != nil {
				t.Errorf("admit: %v", err)
				return
			}
			if allowed {
				require.True(t, reserved)
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.EqualValues(t, 4, admitted.Load(), "all accounts, public models and gateway instances share one million tokens")
	var lease string
	require.NoError(t, db.QueryRow(`SELECT lease_id FROM group_quality_traffic_leases LIMIT 1`).Scan(&lease))
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, lease, true))
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, lease, true))
	allowed, reserved := acquireTraffic(t, r, trafficRequest("replacement", "new-model", "replacement", 250000), 62)
	require.True(t, allowed)
	require.True(t, reserved)
	allowed, _ = acquireTraffic(t, r, trafficRequest("no-room", "new-model", "no-room", 4096), 63)
	require.False(t, allowed)
	s, err := readGroupQualityTrafficSummary(ctx, r.db, p, time.Now(), 0, 0, true)
	require.NoError(t, err)
	require.Equal(t, "bootstrap", s.Mode)
	require.EqualValues(t, 1000000, s.ReservedInputTokens)
	require.Zero(t, s.RemainingTokens)
	require.EqualValues(t, 17, s.LimitedRequests)
}

func TestGroupQualityTrafficAvailabilityModeKeepsServingAfterBudgetExhaustionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	_, err := db.Exec(`UPDATE passive_quality_policies SET config=jsonb_set(config,'{traffic_mode}','"availability"'::jsonb) WHERE id=$1`, p.ID)
	require.NoError(t, err)

	// Exhaust the bootstrap validation allowance with one unverified request.
	allowed, reserved := acquireTraffic(t, r, trafficRequest("validation", "A", "validation", 1_000_000), 62)
	require.True(t, allowed)
	require.True(t, reserved)

	// The next unverified request is ordinary customer traffic in availability
	// mode: it passes hard account/model checks without creating a validation
	// lease or consuming more validation budget.
	allowed, reserved = acquireTraffic(t, r, trafficRequest("ordinary", "A", "ordinary", 4096), 62)
	require.True(t, allowed)
	require.False(t, reserved)
	var leases int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM group_quality_traffic_leases`).Scan(&leases))
	require.Equal(t, 1, leases)
}

func TestGroupQualityTrafficExactAttemptSettlementIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	first := trafficRequest("same-request", "A", "first", 100000)
	second := trafficRequest("same-request", "A", "retry", 100000)
	for _, req := range []service.GroupQualityTrafficRequest{first, second} {
		allowed, reserved := acquireTraffic(t, r, req, 62)
		require.True(t, allowed)
		require.True(t, reserved)
	}
	allowed, _ := acquireTraffic(t, r, first, 62)
	require.True(t, allowed, "idempotent acquire does not spend twice")
	wrong := first
	wrong.Model = "B"
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, wrong, 62, 900000, 800000, true))
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, first, 63, 900000, 800000, true))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM group_quality_traffic_leases WHERE settled_at IS NOT NULL`).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, first, 62, 900000, 800000, true))
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, first, 62, 1, 0, true))
	var input sql.NullInt64
	require.NoError(t, db.QueryRow(`SELECT input_tokens FROM group_quality_traffic_leases WHERE lease_id='retry'`).Scan(&input))
	require.False(t, input.Valid)
	require.NoError(t, db.QueryRow(`SELECT input_tokens FROM group_quality_traffic_leases WHERE lease_id='first'`).Scan(&input))
	require.EqualValues(t, 900000, input.Int64)
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, "retry", false))
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, "retry", true))
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, second, 62, 0, 0, false))
	s, err := readGroupQualityTrafficSummary(ctx, r.db, p, time.Now(), 900000, 800000, true)
	require.NoError(t, err)
	require.EqualValues(t, 900000, s.ValidationInputTokens)
	require.EqualValues(t, 100000, s.ReservedInputTokens)
	require.Zero(t, s.RemainingTokens)
	allowed, _ = acquireTraffic(t, r, trafficRequest("third", "A", "third", 4096), 62)
	require.False(t, allowed, "failed/unknown used attempt never frees its estimate")
	// Resetting a policy cannot reset already-spent group validation tokens.
	p.Name = "edited target"
	require.NoError(t, r.SavePolicy(ctx, &p))
	allowed, _ = acquireTraffic(t, r, trafficRequest("after-edit", "A", "after-edit", 4096), 62)
	require.False(t, allowed)
}

func TestGroupQualityTrafficHealthyModelAndStaleSnapshotIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	healthyTrafficState(t, ctx, db, r, p, "A")
	now := time.Now()
	require.NoError(t, writeGroupQualityTrafficSnapshot(ctx, r.db, p, now, 10000000, 9000000, true))
	allowed, reserved := acquireTraffic(t, r, trafficRequest("healthy", "A", "healthy", 100000000), 62)
	require.True(t, allowed)
	require.True(t, reserved)
	var validation bool
	require.NoError(t, db.QueryRow(`SELECT validation FROM group_quality_traffic_leases WHERE lease_id='healthy'`).Scan(&validation))
	require.False(t, validation)
	allowed, _ = acquireTraffic(t, r, trafficRequest("other-model", "B", "other-model", 600000), 62)
	require.False(t, allowed, "another public model cannot borrow the healthy bypass")
	allowed, _ = acquireTraffic(t, r, trafficRequest("other-account", "A", "other-account", 600000), 63)
	require.False(t, allowed)
	_, err := db.Exec(`UPDATE group_quality_traffic_snapshots SET measured_at=now()-interval '3 minutes'`)
	require.NoError(t, err)
	allowed, _ = acquireTraffic(t, r, trafficRequest("stale", "B", "stale", 200000), 63)
	require.False(t, allowed, "stale read headroom cannot open validation capacity")
	require.NoError(t, writeGroupQualityTrafficSnapshot(ctx, r.db, p, time.Now(), 10000000, 9000000, true))
	// Prove admission does not query the large real usage table.
	_, err = db.Exec(`ALTER TABLE usage_logs RENAME TO usage_logs_admission_must_not_read`)
	require.NoError(t, err)
	allowed, _ = acquireTraffic(t, r, trafficRequest("fresh", "B", "fresh", 200000), 63)
	require.True(t, allowed)
	_, err = db.Exec(`ALTER TABLE usage_logs_admission_must_not_read RENAME TO usage_logs`)
	require.NoError(t, err)
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, trafficRequest("healthy", "A", "healthy", 100000000), 62, 1000, 900, true))
	s, err := readGroupQualityTrafficSummary(ctx, r.db, p, time.Now(), 10000000, 9000000, true)
	require.NoError(t, err)
	require.EqualValues(t, 1, s.VerifiedAccounts)
	require.Zero(t, s.ValidationInputTokens, "healthy usage is ledgered but not validation")
}

func TestGroupQualityTrafficProjectionAndMembershipFencesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	healthyTrafficState(t, ctx, db, r, p, "A")
	readExtra := func() map[string]any {
		var raw []byte
		require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=62`).Scan(&raw))
		var extra map[string]any
		require.NoError(t, json.Unmarshal(raw, &extra))
		return extra
	}
	entries := readExtra()["group_quality_targets"].([]any)
	require.Len(t, entries, 1)
	entry := entries[0].(map[string]any)
	require.Equal(t, true, entry["verified"])
	require.Equal(t, "A", entry["model"])
	require.EqualValues(t, 8, entry["group_id"])
	until, err := time.Parse(time.RFC3339Nano, entry["verified_until"].(string))
	require.NoError(t, err)
	require.True(t, until.After(time.Now()))
	p.Name = "new version"
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.Contains(t, readExtra(), "group_quality_targets", "a rename preserves verified evidence")
	p.CacheRate.Pause = 81
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.NotContains(t, readExtra(), "group_quality_targets")
	healthyTrafficState(t, ctx, db, r, p, "A")
	_, err = db.Exec(`UPDATE passive_quality_states SET data=jsonb_set(data,'{updated_at}',to_jsonb(now()-interval '2 hours')) WHERE policy_id=$1`, p.ID)
	require.NoError(t, err)
	allowed, _ := acquireTraffic(t, r, trafficRequest("expired", "A", "expired", 2000000), 62)
	require.False(t, allowed)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = lockPassiveAccount(ctx, tx, 62)
	require.NoError(t, err)
	_, err = tx.Exec(`DELETE FROM account_groups WHERE account_id=62 AND group_id=8`)
	require.NoError(t, err)
	require.NoError(t, releaseDetachedPassiveProtection(ctx, tx, 62))
	require.NoError(t, tx.Commit())
	require.NotContains(t, readExtra(), "group_quality_targets")
	allowed, _ = acquireTraffic(t, r, trafficRequest("detached", "A", "detached", 4096), 62)
	require.False(t, allowed)
}

func TestGroupQualityTrafficFullPopulationSnapshotAndRetentionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	insertGroupTargetUsage(t, db, r, "known", "A", 8, time.Now().Add(-time.Second), 90, true)
	insertGroupTargetUsage(t, db, r, "unknown", "B", 8, time.Now().Add(-time.Second), 0, false)
	_, err := db.Exec(`UPDATE usage_logs SET input_tokens=900 WHERE request_id='unknown'`)
	require.NoError(t, err)
	now := time.Now()
	computed, err := r.groupQualityTargetSummary(ctx, p, now)
	require.NoError(t, err)
	require.NoError(t, r.publishGroupQualitySummary(ctx, p, computed))
	out, err := r.GroupQualityTargetSummaries(ctx, now)
	require.NoError(t, err)
	require.Len(t, out, 1)
	require.EqualValues(t, 1000, out[0].TrafficControl.TotalInputTokens, "missing cache report remains in budget denominator")
	var input, read int64
	require.NoError(t, db.QueryRow(`SELECT input_tokens,read_tokens FROM group_quality_traffic_snapshots WHERE policy_id=$1`, p.ID).Scan(&input, &read))
	require.EqualValues(t, 1000, input)
	require.EqualValues(t, 90, read)
	old := p
	p.Name = "changed"
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.NoError(t, writeGroupQualityTrafficSnapshot(ctx, r.db, old, time.Now(), 999999999, 999999999, true))
	require.NoError(t, db.QueryRow(`SELECT input_tokens FROM group_quality_traffic_snapshots WHERE policy_id=$1`, p.ID).Scan(&input))
	require.EqualValues(t, 1000, input, "stale policy version cannot refresh")
	_, err = db.Exec(`INSERT INTO group_quality_traffic_leases(lease_id,policy_id,policy_version,account_id,group_id,model,request_key,validation,reserved_input,created_at)
 SELECT 'old-'||i,$1,$2,62,8,'A','old-'||i,true,500000,now()-interval '3 days' FROM generate_series(1,5001) i`, p.ID, p.Version)
	require.NoError(t, err)
	allowed, _ := acquireTraffic(t, r, trafficRequest("live", "A", "live", 1000000), 62)
	require.True(t, allowed, "unknown reservations age out after one hour")
	require.NoError(t, r.PrunePassiveQuality(ctx))
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM group_quality_traffic_leases WHERE created_at<now()-interval '2 days'`).Scan(&count))
	require.Equal(t, 1, count, "housekeeping is bounded at five thousand rows")
}

func TestGroupQualityTrafficKnownUsageReplacesEstimateIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	req := trafficRequest("measured", "A", "measured", 400000)
	allowed, _ := acquireTraffic(t, r, req, 62)
	require.True(t, allowed)
	wrong := req
	wrong.GroupID = 9
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, wrong, 62, 1, 1, true))
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, req, 62, 100000, 200000, true))
	var read int64
	require.NoError(t, db.QueryRow(`SELECT read_tokens FROM group_quality_traffic_leases WHERE lease_id='measured'`).Scan(&read))
	require.EqualValues(t, 100000, read, "read cannot exceed actual input")
	s, err := readGroupQualityTrafficSummary(ctx, r.db, p, time.Now(), 100000, 100000, true)
	require.NoError(t, err)
	require.EqualValues(t, 100000, s.ValidationInputTokens)
	require.Zero(t, s.ReservedInputTokens)
	require.EqualValues(t, 900000, s.RemainingTokens)
	allowed, _ = acquireTraffic(t, r, req, 62)
	require.False(t, allowed, "settled attempt is never reopened")
	allowed, _ = acquireTraffic(t, r, trafficRequest("next", "A", "next", 900000), 63)
	require.True(t, allowed)
}

func TestGroupQualityTrafficUnknownPositiveAndZeroUsageKeepEstimateIntegration(t *testing.T) {
	for _, tc := range []struct {
		name     string
		input    int64
		reported bool
	}{{"missing-report", 100000, false}, {"zero-report", 0, true}} {
		t.Run(tc.name, func(t *testing.T) {
			ctx, db, r := passiveDB(t)
			p := newGroupTargetPolicy(t, ctx, db, r)
			req := trafficRequest("unknown", "A", "unknown", 400000)
			allowed, _ := acquireTraffic(t, r, req, 62)
			require.True(t, allowed)
			require.NoError(t, r.SettleGroupQualityTraffic(ctx, req, 62, tc.input, 0, tc.reported))
			require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, req.LeaseID, true))
			s, err := readGroupQualityTrafficSummary(ctx, r.db, p, time.Now(), tc.input, 0, true)
			require.NoError(t, err)
			require.Zero(t, s.ValidationInputTokens)
			require.EqualValues(t, 400000, s.ReservedInputTokens)
			require.EqualValues(t, 600000, s.RemainingTokens)
			allowed, _ = acquireTraffic(t, r, trafficRequest("too-large", "A", "too-large", 650000), 62)
			require.False(t, allowed)
		})
	}
}

func TestGroupQualityTrafficConcurrentSettlementAndUnusedReleaseIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	req := trafficRequest("race", "A", "race", 500000)
	allowed, _ := acquireTraffic(t, r, req, 62)
	require.True(t, allowed)
	var wg sync.WaitGroup
	for i := 0; i < 20; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			var err error
			if i%2 == 0 {
				err = r.SettleGroupQualityTraffic(ctx, req, 62, 100000, 90000, true)
			} else {
				err = r.ReleaseGroupQualityTraffic(ctx, req.LeaseID, true)
			}
			if err != nil {
				t.Errorf("settlement race: %v", err)
			}
		}(i)
	}
	wg.Wait()
	var unused bool
	var settled sql.NullTime
	require.NoError(t, db.QueryRow(`SELECT unused,settled_at FROM group_quality_traffic_leases WHERE lease_id='race'`).Scan(&unused, &settled))
	require.NotEqual(t, unused, settled.Valid, "a reservation can be unused or settled, never both")
	s, err := readGroupQualityTrafficSummary(ctx, r.db, p, time.Now(), 100000, 90000, true)
	require.NoError(t, err)
	require.Zero(t, s.ReservedInputTokens)
	if unused {
		require.EqualValues(t, 1000000, s.RemainingTokens)
	} else {
		require.EqualValues(t, 900000, s.RemainingTokens)
	}
}
