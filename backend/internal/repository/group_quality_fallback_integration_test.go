//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func fallbackTrafficContext(req service.GroupQualityTrafficRequest, aid int64) context.Context {
	req.AccountID = aid
	return service.WithGroupQualityFallback(service.GroupQualityTrafficContext(context.Background(), req), service.GroupQualityFallbackRequest{GroupID: req.GroupID, AccountID: aid, Model: req.Model})
}
func acquireFallback(t *testing.T, r *passiveQualityRepository, req service.GroupQualityTrafficRequest, aid int64) (bool, bool) {
	t.Helper()
	allowed, reserved, err := r.AcquireGroupQualityTraffic(fallbackTrafficContext(req, aid), aid, req.Model, req.LeaseID)
	require.NoError(t, err)
	return allowed, reserved
}
func TestGroupQualityTrafficNonOpenAIHasNoPartialBudgetIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	_, err := db.Exec(`UPDATE groups SET platform='claude' WHERE id=8`)
	require.NoError(t, err)
	// Generic fallback may set group ID, without body/model/usage integration.
	req := trafficRequest("generic", "", "generic", 65536)
	allowed, reserved := acquireTraffic(t, r, req, 62)
	require.True(t, allowed)
	require.False(t, reserved)
	req.Model = "A"
	allowed, reserved = acquireFallback(t, r, req, 62)
	require.False(t, allowed)
	require.False(t, reserved)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM group_quality_traffic_leases`).Scan(&count))
	require.Zero(t, count)
	s, err := r.groupQualityTargetSummary(ctx, p, time.Now())
	require.NoError(t, err)
	require.False(t, s.TrafficControl.Enabled)
	require.False(t, s.Fallback.Enabled)
}

func TestGroupQualityFallbackConcurrentAdmissionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	allowed, reserved := acquireTraffic(t, r, trafficRequest("spent", "A", "spent", 1000000), 62)
	require.True(t, allowed)
	require.True(t, reserved)
	allowed, _ = acquireTraffic(t, r, trafficRequest("normal-no-room", "A", "normal-no-room", 4096), 62)
	require.False(t, allowed, "ordinary validation is still subject to the exhausted hour budget")
	const requests = 20
	start := make(chan struct{})
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < requests; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			<-start
			req := trafficRequest(fmt.Sprint("emergency-", i), fmt.Sprintf("model-%d", i%3), fmt.Sprint("emergency-", i), 70000)
			aid := int64(62 + i%2)
			other := &passiveQualityRepository{db: db}
			ok, reserved, err := other.AcquireGroupQualityTraffic(fallbackTrafficContext(req, aid), aid, req.Model, req.LeaseID)
			if err != nil {
				t.Errorf("admit: %v", err)
				return
			}
			if ok {
				if !reserved {
					t.Error("missing reservation")
				}
				admitted.Add(1)
			}
		}(i)
	}
	close(start)
	wg.Wait()
	require.EqualValues(t, requests, admitted.Load(), "all gateway instances, accounts and models may fall back concurrently, including repeated account/model pairs")
	s, err := r.groupQualityFallbackSummary(ctx, p, time.Now())
	require.NoError(t, err)
	require.Equal(t, requests, s.InFlight)
	require.EqualValues(t, requests, s.Requests)
	require.EqualValues(t, requests*70000, s.ReservedInputTokens)
	require.Zero(t, s.PacingSeconds)
	require.NotNil(t, s.LastAccountID)
	require.NotNil(t, s.LastUsedAt)

	// A second gateway may retry the same attempt without counting or charging it twice.
	other := &passiveQualityRepository{db: db}
	req := trafficRequest("emergency-0", "model-0", "emergency-0", 70000)
	allowed, reserved = acquireFallback(t, other, req, 62)
	require.True(t, allowed)
	require.True(t, reserved)
	for _, changed := range []service.GroupQualityTrafficRequest{
		trafficRequest("another-request", req.Model, req.LeaseID, 70000),
		trafficRequest(req.Key, "another-model", req.LeaseID, 70000),
	} {
		allowed, _ = acquireFallback(t, other, changed, 62)
		require.False(t, allowed, "an existing reservation must retain its exact request and model")
	}
	allowed, _ = acquireFallback(t, other, req, 63)
	require.False(t, allowed, "an existing reservation cannot move to another account")
	s, err = r.groupQualityFallbackSummary(ctx, p, time.Now())
	require.NoError(t, err)
	require.Equal(t, requests, s.InFlight)
	require.EqualValues(t, requests, s.Requests)
	require.EqualValues(t, requests*70000, s.ReservedInputTokens)
}

func TestGroupQualityFallbackIndependentLeaseLifecycleIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	first := trafficRequest("first", "A", "first", 70000)
	allowed, reserved := acquireFallback(t, r, first, 62)
	require.True(t, allowed)
	require.True(t, reserved)
	_, err := db.Exec(`UPDATE group_quality_traffic_leases SET fallback_expires_at=now()+interval '1 second' WHERE lease_id=$1`, first.LeaseID)
	require.NoError(t, err)
	other := &passiveQualityRepository{db: db}
	renewed, err := other.RefreshGroupQualityFallback(ctx, first.LeaseID)
	require.NoError(t, err)
	require.True(t, renewed)
	var expires time.Time
	require.NoError(t, db.QueryRow(`SELECT fallback_expires_at FROM group_quality_traffic_leases WHERE lease_id=$1`, first.LeaseID).Scan(&expires))
	require.True(t, expires.After(time.Now().Add(119*time.Second)), "renewal extends the still-owned lease")

	second := trafficRequest("second", "A", "second", 4096)
	allowed, reserved = acquireFallback(t, other, second, 62)
	require.True(t, allowed, "same-account same-model fallback does not wait for an in-flight request to release")
	require.True(t, reserved)
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, first.LeaseID, false))
	renewed, err = r.RefreshGroupQualityFallback(ctx, first.LeaseID)
	require.NoError(t, err)
	require.False(t, renewed)
	allowed, _ = acquireFallback(t, r, first, 62)
	require.False(t, allowed, "released attempts cannot be revived")
	renewed, err = r.RefreshGroupQualityFallback(ctx, second.LeaseID)
	require.NoError(t, err)
	require.True(t, renewed, "releasing one request does not invalidate another request's lease")
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, second.LeaseID, true))
	// A future legacy timestamp makes the lack of a pacing gate deterministic,
	// even if the test process is suspended between these sequential admissions.
	_, err = db.Exec(`UPDATE group_quality_traffic_snapshots SET last_fallback_at=now()+interval '1 minute' WHERE policy_id=$1`, p.ID)
	require.NoError(t, err)
	next := trafficRequest("next", "A", "next", 4096)
	allowed, _ = acquireFallback(t, r, next, 62)
	require.True(t, allowed, "fallback does not wait for the old two-second pacing interval")
	_, err = db.Exec(`UPDATE group_quality_traffic_leases SET fallback_expires_at=now()-interval '1 second' WHERE lease_id='next'`)
	require.NoError(t, err)
	renewed, err = r.RefreshGroupQualityFallback(ctx, "next")
	require.NoError(t, err)
	require.False(t, renewed, "expired owner never revives")
	allowed, _ = acquireFallback(t, r, next, 62)
	require.False(t, allowed, "expired attempts cannot be reacquired")
	allowed, _ = acquireFallback(t, r, trafficRequest("crash-replacement", "B", "crash-replacement", 4096), 63)
	require.True(t, allowed)
	s, err := r.groupQualityFallbackSummary(ctx, p, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, s.InFlight)
	require.EqualValues(t, 3, s.Requests)
	require.EqualValues(t, 78192, s.ReservedInputTokens)
}

func TestGroupQualityFallbackHealthySupplyAndExactModelIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	allowed, _ := acquireFallback(t, r, trafficRequest("before-recovery", "A", "before-recovery", 4096), 63)
	require.True(t, allowed)
	healthyTrafficState(t, ctx, db, r, p, "A")
	allowed, _ = acquireFallback(t, r, trafficRequest("same-model", "A", "same-model", 4096), 63)
	require.False(t, allowed, "recovered healthy supply closes new fallback admission for that exact model")
	allowed, _ = acquireFallback(t, r, trafficRequest("other-model", "B", "other-model", 4096), 63)
	require.True(t, allowed)
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, "other-model", true))
	_, err := db.Exec(`UPDATE accounts SET schedulable=false WHERE id=62`)
	require.NoError(t, err)
	allowed, _ = acquireFallback(t, r, trafficRequest("unavailable-healthy", "A", "unavailable-healthy", 4096), 63)
	require.True(t, allowed, "manually disabled healthy state is not usable supply")
}

func TestGroupQualityFallbackHardAndActiveProofFencesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	newGroupTargetPolicy(t, ctx, db, r)
	_, err := db.Exec(`ALTER TABLE accounts ADD COLUMN platform TEXT DEFAULT 'openai',ADD COLUMN type TEXT DEFAULT 'apikey',ADD COLUMN credentials JSONB DEFAULT '{}',ADD COLUMN overload_until TIMESTAMPTZ,ADD COLUMN expires_at TIMESTAMPTZ,ADD COLUMN auto_pause_on_expired BOOLEAN DEFAULT true,ADD COLUMN parent_account_id BIGINT;INSERT INTO scheduled_test_protection_states(account_id,blocked) VALUES(62,true)`)
	require.NoError(t, err)
	cases := []struct{ name, patch string }{
		{"manual", "schedulable=false"}, {"auth_error", "status='error'"}, {"manual_disabled", "status='disabled'"},
		{"expired", "expires_at=now()-interval '1 minute'"}, {"overload", "overload_until=now()+interval '1 hour'"},
		{"rate_limit", "rate_limit_reset_at=now()+interval '1 hour'"}, {"temporary", "temp_unschedulable_until=now()+interval '1 hour'"},
		{"quota", `extra='{"quota_limit":1,"quota_used":2}'`}, {"unsupported_model", `credentials='{"model_mapping":{"other":"other"}}'`},
		{"shadow", "parent_account_id=63"}, {"deleted", "deleted_at=now()"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`UPDATE accounts SET status='quality_paused',schedulable=true,deleted_at=NULL,extra='{}',credentials='{}',expires_at=NULL,overload_until=NULL,rate_limit_reset_at=NULL,temp_unschedulable_until=NULL,parent_account_id=NULL WHERE id=62`)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE accounts SET ` + tc.patch + ` WHERE id=62`)
			require.NoError(t, err)
			allowed, _ := acquireFallback(t, r, trafficRequest(tc.name, "A", tc.name, 4096), 62)
			require.False(t, allowed)
		})
	}
	_, err = db.Exec(`UPDATE accounts SET status='quality_paused',schedulable=true,deleted_at=NULL,extra='{}',credentials='{}',expires_at=NULL,overload_until=NULL,rate_limit_reset_at=NULL,temp_unschedulable_until=NULL,parent_account_id=NULL WHERE id=62;DELETE FROM scheduled_test_protection_states`)
	require.NoError(t, err)
	allowed, _ := acquireFallback(t, r, trafficRequest("stale-proof", "A", "stale-proof", 4096), 62)
	require.False(t, allowed)
	_, err = db.Exec(`INSERT INTO scheduled_test_combination_states(account_id,blocked) VALUES(62,true)`)
	require.NoError(t, err)
	allowed, _ = acquireFallback(t, r, trafficRequest("proven-active", "A", "proven-active", 4096), 62)
	require.True(t, allowed)
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, "proven-active", true))
	_, err = db.Exec(`DELETE FROM account_groups WHERE account_id=62 AND group_id=8`)
	require.NoError(t, err)
	allowed, _ = acquireFallback(t, r, trafficRequest("detached", "A", "detached", 4096), 62)
	require.False(t, allowed)
}

func TestGroupQualityFallbackAutomaticPassiveAndMissingPolicyIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	unrelated := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &unrelated))
	for _, policy := range []service.PassiveQualityPolicy{p, unrelated} {
		state := service.PassiveQualityState{PolicyID: policy.ID, AccountID: 62, Model: "A", Scope: policy.Scope, Version: policy.Version, Phase: "paused", Purpose: policy.Purpose}
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, savePassiveState(ctx, tx, state))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
		require.NoError(t, tx.Commit())
	}
	req := trafficRequest("passive", "A", "passive", 4096)
	allowed, _, err := r.AcquirePassiveQuality(service.GroupQualityTrafficContext(ctx, req), 62, "A", "normal")
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, reserved, err := r.AcquirePassiveQuality(fallbackTrafficContext(req, 62), 62, "A", "passive-trial")
	require.NoError(t, err)
	require.True(t, allowed)
	require.False(t, reserved)
	allowed, _ = acquireFallback(t, r, req, 62)
	require.True(t, allowed)
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, req.LeaseID, true))
	p.Enabled = false
	require.NoError(t, r.SavePolicy(ctx, &p))
	allowed, reserved = acquireFallback(t, r, trafficRequest("no-policy", "A", "no-policy", 4096), 62)
	require.False(t, allowed)
	require.False(t, reserved)
}

func TestGroupQualityFallbackCacheDisabledAccountingAndFinalGateIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.CacheRate = service.PassiveQualityMetric{}
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 2, Recover: 1}
	require.NoError(t, r.SavePolicy(ctx, &p))
	_, err := db.Exec(`UPDATE accounts SET status='quality_paused' WHERE id=62;INSERT INTO scheduled_test_protection_states(account_id,blocked) VALUES(62,true)`)
	require.NoError(t, err)
	req := trafficRequest("active", "A", "active", 50000)
	req.AccountID = 62
	gate := &accountRepository{sql: db}
	allowed, err := gate.AcquireCacheRecoveryRequest(fallbackTrafficContext(req, 62), 62)
	require.NoError(t, err)
	require.False(t, allowed, "marker alone cannot bypass active quality gate")
	allowed, _ = acquireFallback(t, r, req, 62)
	require.True(t, allowed)
	allowed, err = gate.AcquireCacheRecoveryRequest(fallbackTrafficContext(req, 62), 62)
	require.NoError(t, err)
	require.True(t, allowed)
	wrong := req
	wrong.Key = "wrong"
	allowed, err = gate.AcquireCacheRecoveryRequest(fallbackTrafficContext(wrong, 62), 62)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = db.Exec(`UPDATE accounts SET schedulable=false WHERE id=62`)
	require.NoError(t, err)
	allowed, err = gate.AcquireCacheRecoveryRequest(fallbackTrafficContext(req, 62), 62)
	require.NoError(t, err)
	require.False(t, allowed)
	_, err = db.Exec(`UPDATE accounts SET schedulable=true WHERE id=62;DELETE FROM scheduled_test_protection_states`)
	require.NoError(t, err)
	allowed, err = gate.AcquireCacheRecoveryRequest(fallbackTrafficContext(req, 62), 62)
	require.NoError(t, err)
	require.False(t, allowed, "stale active proof remains fail closed")
	require.NoError(t, r.SettleGroupQualityTraffic(ctx, req, 62, 12000, 10000, true))
	require.NoError(t, r.ReleaseGroupQualityTraffic(ctx, req.LeaseID, false))
	s, err := r.groupQualityTargetSummary(ctx, p, time.Now())
	require.NoError(t, err)
	require.False(t, s.TrafficControl.Enabled)
	require.True(t, s.Fallback.Enabled)
	require.EqualValues(t, 1, s.Fallback.Requests)
	require.EqualValues(t, 12000, s.Fallback.InputTokens)
	require.Zero(t, s.Fallback.ReservedInputTokens)
	require.Zero(t, s.Fallback.InFlight)
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	require.Contains(t, string(raw), `"fallback":{"enabled":true`)
}

func TestGroupQualityFallbackMarkerAndExclusionFencesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	req := trafficRequest("exact", "A", "exact", 4096)
	for _, marker := range []service.GroupQualityFallbackRequest{
		{GroupID: 8, AccountID: 63, Model: "A"},
		{GroupID: 9, AccountID: 62, Model: "A"},
		{GroupID: 8, AccountID: 62, Model: "B"},
	} {
		marked := service.WithGroupQualityFallback(service.GroupQualityTrafficContext(ctx, req), marker)
		allowed, reserved, err := r.AcquireGroupQualityTraffic(marked, 62, "A", req.LeaseID)
		require.NoError(t, err)
		require.False(t, allowed)
		require.False(t, reserved)
	}
	marker := service.GroupQualityFallbackRequest{GroupID: 8, AccountID: 62, Model: "A"}
	for _, marked := range []context.Context{
		service.WithGroupQualityFallback(ctx, marker),
		service.WithGroupQualityFallback(service.GroupQualityTrafficContext(ctx, service.GroupQualityTrafficRequest{Key: "image", GroupID: 8, Model: "A", NonText: true}), marker),
	} {
		allowed, reserved, err := r.AcquireGroupQualityTraffic(marked, 62, "A", req.LeaseID)
		require.NoError(t, err)
		require.False(t, allowed)
		require.False(t, reserved)
	}
	p.ExcludedAccountIDs = []int64{62}
	require.NoError(t, r.SavePolicy(ctx, &p))
	allowed, reserved := acquireFallback(t, r, req, 62)
	require.False(t, allowed)
	require.False(t, reserved)
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM group_quality_traffic_leases`).Scan(&count))
	require.Zero(t, count)
}
