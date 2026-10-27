//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func v2CachePolicy() service.PassiveQualityPolicy {
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.ErrorRate.Enabled = false
	p.CacheRate = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 20}
	c := service.DefaultPassiveQualityCacheConfig()
	c.Action = "reduce"
	w := service.PassiveQualityCacheWindow{WindowSeconds: 120, MaxSamples: 100, MinSamples: 6, Confirmations: 2, NewSamples: 2, MinTokens: 2048, MinSessions: 2, MaxSessionSharePercent: 80}
	c.Quick = w
	c.ActionWindow = w
	c.MinHoldSeconds = 30
	c.RecoveryHoldSeconds = 30
	c.RecoveryConfirmations = 2
	p.CacheProtection = &c
	return p
}

func TestPassiveQualityCacheEvidenceDoesNotInventReuseIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	recordCacheRequest(t, ctx, db, r, 62, "failed-prior", "fail-session", "prefix", base, 0)
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: "failed-prior", StartedAt: base.Add(-time.Second), At: base}}, nil))
	recordCacheRequest(t, ctx, db, r, 62, "after-failure", "fail-session", "prefix", base.Add(time.Second), 0)
	recordCacheRequest(t, ctx, db, r, 62, "concurrent-1", "concurrent", "prefix", base.Add(2*time.Second), 0)
	recordCacheRequest(t, ctx, db, r, 62, "concurrent-2", "concurrent", "prefix", base.Add(2*time.Second), 0)
	recordCacheRequest(t, ctx, db, r, 62, "unverified", "wire-session", "prefix", base.Add(3*time.Second), 0)
	_, err := db.Exec(`UPDATE passive_quality_cache_reports SET cache_metadata=jsonb_set(cache_metadata,'{wire_verified}','false') WHERE request_key='unverified'`)
	require.NoError(t, err)
	recordCacheRequest(t, ctx, db, r, 62, "after-unverified", "wire-session", "prefix", base.Add(4*time.Second), 0)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	samples, _, err := passiveCacheSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62}, base.Add(5*time.Second))
	require.NoError(t, err)
	got := map[string]service.PassiveQualitySample{}
	for _, sample := range samples {
		got[sample.Key] = sample
	}
	require.NotContains(t, got, "failed-prior")
	for _, key := range []string{"after-failure", "concurrent-1", "concurrent-2", "after-unverified"} {
		require.Equal(t, "cold", got[key].CacheOpportunity, key)
	}
	require.Equal(t, "unknown", got["unverified"].CacheOpportunity)
	require.NoError(t, tx.Rollback())
	migration, err := os.ReadFile("../../migrations/301_passive_quality_reuse_evidence.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err, "additive migration is repeatable")
}

func TestPassiveQualityLatencyRecoveryLeavesCacheHoldIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	p.TTFT = service.PassiveQualityMetric{Enabled: true, Pause: 100, Recover: 80}
	p.TTFTConcurrencyLimit = 2
	p.TTFTPriorityPenalty = 10
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	seed := func(prefix string, start time.Time, count int, latency int) {
		for i := 0; i < count; i++ {
			_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms)VALUES(62,$1,$2,$3)`, fmt.Sprintf("%s-%d", prefix, i), start.Add(time.Duration(i)*time.Millisecond), latency)
			require.NoError(t, err)
		}
	}
	seed("slow", base, 5, 150)
	_, err := r.advance(ctx, p.ID, 62, base.Add(time.Second), false)
	require.NoError(t, err)
	seed("slow-new", base.Add(2*time.Second), 2, 150)
	_, err = r.advance(ctx, p.ID, 62, base.Add(3*time.Second), false)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "limited", states[0].Latency.Phase)
	cap, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Equal(t, 2, cap)
	// A second policy's independent cache restriction survives TTFT recovery.
	q := v2CachePolicy()
	q.Name = "cache-owner"
	require.NoError(t, r.SavePolicy(ctx, &q))
	s := service.PassiveQualityState{PolicyID: q.ID, PolicyName: q.Name, AccountID: 62, Scope: q.Scope, Model: q.Model, Mode: q.Mode, Version: q.Version, Phase: "healthy", UpdatedAt: base, Cache: map[string]service.PassiveQualityCacheState{"model": {Phase: "reduced", AllocationPercent: 25}}}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, savePassiveState(ctx, tx, s))
	require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
	require.NoError(t, tx.Commit())
	seed("fast", base.Add(40*time.Second), 5, 50)
	_, err = r.advance(ctx, p.ID, 62, base.Add(41*time.Second), false)
	require.NoError(t, err)
	seed("fast-new", base.Add(42*time.Second), 2, 50)
	_, err = r.advance(ctx, p.ID, 62, base.Add(43*time.Second), false)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "healthy", states[0].Latency.Phase)
	require.Equal(t, "reduced", states[1].Cache["model"].Phase)
	cap, err = r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Zero(t, cap)
	require.NoError(t, r.Act(ctx, p.ID, 62, "exempt", 60))
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=62`).Scan(&raw))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(raw, &extra))
	a := service.Account{Extra: extra}
	require.Equal(t, 25, a.PassiveQualityNewSessionPercent("model"), "one policy exemption cannot clear another")
}

func recordCacheRequest(t *testing.T, ctx context.Context, db *sql.DB, r *passiveQualityRepository, aid int64, key, session, prefix string, at time.Time, read int64) {
	t.Helper()
	metadata := &service.PassiveQualityCacheMetadata{Version: 1, WireVerified: true, AccountID: aid, SessionHash: session, Model: "model", Scope: fmt.Sprintf("scope-%d", aid), StartedAt: at.Add(-100 * time.Millisecond), PrefixHashes: []string{prefix}, PromptHash: prefix, MinimumTokens: 1024, RetentionSeconds: 300}
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: aid, RequestKey: key, StartedAt: metadata.StartedAt, At: at, CacheReported: true, CacheMetadata: metadata}}, nil))
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,input_tokens,cache_read_tokens,first_token_ms,duration_ms)VALUES($1,$2,$3,'model',$4,$5,50,100)`, aid, key, at, 2048-read, read)
	require.NoError(t, err)
}

func TestPassiveQualityCacheReuseEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	recordCacheRequest(t, ctx, db, r, 62, "first", "s", "p", base, 0)
	recordCacheRequest(t, ctx, db, r, 62, "zero-hit-reuse", "s", "p", base.Add(time.Second), 0)
	recordCacheRequest(t, ctx, db, r, 63, "switch-b", "s", "p", base.Add(2*time.Second), 0)
	recordCacheRequest(t, ctx, db, r, 62, "switch-back-a", "s", "p", base.Add(3*time.Second), 0)
	recordCacheRequest(t, ctx, db, r, 62, "a-warmed", "s", "p", base.Add(4*time.Second), 0)
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,cache_read_tokens)VALUES(62,'legacy',$1,100)`, base.Add(5*time.Second))
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	samples, complete, err := passiveCacheSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62}, base.Add(6*time.Second))
	require.NoError(t, err)
	require.True(t, complete)
	got := map[string]service.PassiveQualitySample{}
	for _, v := range samples {
		got[v.Key] = v
	}
	require.Equal(t, "cold", got["first"].CacheOpportunity)
	require.Equal(t, "reusable", got["zero-hit-reuse"].CacheOpportunity, "previous positive hit is not required")
	require.EqualValues(t, 2048, got["zero-hit-reuse"].CacheExpectedTokens)
	require.Equal(t, "cold", got["switch-back-a"].CacheOpportunity)
	require.Equal(t, "account_switched", got["switch-back-a"].CacheReason)
	require.Equal(t, "reusable", got["a-warmed"].CacheOpportunity)
	require.Equal(t, "unknown", got["legacy"].CacheOpportunity)
	require.Equal(t, "reuse_history_unavailable", got["legacy"].CacheReason)
	require.NoError(t, tx.Rollback())
	// A longer configured TTL cannot extend a known provider's cache retention.
	p.CacheProtection.ReuseWindowSeconds = 3600
	p.CacheProtection.ActionWindow.WindowSeconds = 900
	p.CacheProtection.Quick.WindowSeconds = 900
	recordCacheRequest(t, ctx, db, r, 62, "expired", "s", "p", base.Add(601*time.Second), 0)
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	samples, _, err = passiveCacheSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62}, base.Add(602*time.Second))
	require.NoError(t, err)
	require.Equal(t, "cold", samples[0].CacheOpportunity)
	require.Equal(t, "reuse_expired", samples[0].CacheReason)
}

func TestPassiveQualityCacheLimitsRecoverWithoutPausingIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	seed := func(prefix string, start time.Time, count int, read int64) {
		for i := 0; i < count; i++ {
			recordCacheRequest(t, ctx, db, r, 62, fmt.Sprintf("%s-%d", prefix, i), fmt.Sprintf("session-%d", i%2), "prompt", start.Add(time.Duration(i)*time.Second), read)
		}
	}
	seed("bad", base, 8, 0)
	_, err := r.advance(ctx, p.ID, 62, base.Add(9*time.Second), false)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, 1, states[0].Cache["model"].Hits)
	_, err = r.advance(ctx, p.ID, 62, base.Add(10*time.Second), false)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, 1, states[0].Cache["model"].Hits, "re-reading cannot confirm")
	seed("bad-new", base.Add(11*time.Second), 2, 0)
	_, err = r.advance(ctx, p.ID, 62, base.Add(13*time.Second), false)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "reduced", states[0].Cache["model"].Phase)
	require.Equal(t, "healthy", states[0].Phase)
	cap, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Zero(t, cap)
	ok, leased, err := r.AcquirePassiveQuality(ctx, 62, "model", "cache-existing-session")
	require.NoError(t, err)
	require.True(t, ok)
	require.False(t, leased)
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=62`).Scan(&raw))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(raw, &extra))
	a := service.Account{Extra: extra}
	require.False(t, a.PassiveQualityBlocked("model"))
	require.Equal(t, 25, a.PassiveQualityNewSessionPercent("model"))
	// No traffic is insufficient, never automatic healthy or pause.
	_, err = r.advance(ctx, p.ID, 62, base.Add(40*time.Second), false)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "reduced", states[0].Cache["model"].Phase)
	seed("healthy", base.Add(44*time.Second), 6, 1024)
	_, err = r.advance(ctx, p.ID, 62, base.Add(51*time.Second), false)
	require.NoError(t, err)
	seed("healthy-new", base.Add(52*time.Second), 2, 1024)
	_, err = r.advance(ctx, p.ID, 62, base.Add(55*time.Second), false)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "recovering", states[0].Cache["model"].Phase)
	require.Equal(t, 50, states[0].Cache["model"].AllocationPercent)
	require.NoError(t, r.Act(ctx, p.ID, 62, "exempt", 60))
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Empty(t, states[0].Cache)
}

func TestPassiveQualityCachePoolConcurrentPoliciesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	q := v2CachePolicy()
	q.Name = "overlap"
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.NoError(t, r.SavePolicy(ctx, &q))
	base := q.UpdatedAt.Add(time.Second)
	for _, aid := range []int64{62, 63} {
		for i := 0; i < 8; i++ {
			recordCacheRequest(t, ctx, db, r, aid, fmt.Sprintf("%d-old-%d", aid, i), fmt.Sprintf("%d-s-%d", aid, i%2), "p", base.Add(time.Duration(i)*time.Second), 0)
		}
	}
	for _, policy := range []service.PassiveQualityPolicy{p, q} {
		for _, aid := range []int64{62, 63} {
			_, err := r.advance(ctx, policy.ID, aid, base.Add(9*time.Second), false)
			require.NoError(t, err)
		}
	}
	for _, aid := range []int64{62, 63} {
		for i := 0; i < 2; i++ {
			recordCacheRequest(t, ctx, db, r, aid, fmt.Sprintf("%d-new-%d", aid, i), fmt.Sprintf("%d-s-%d", aid, i), "p", base.Add(time.Duration(11+i)*time.Second), 0)
		}
	}
	var wg sync.WaitGroup
	errs := make(chan error, 4)
	for _, pid := range []int64{p.ID, q.ID} {
		for _, aid := range []int64{62, 63} {
			wg.Add(1)
			go func(pid, aid int64) {
				defer wg.Done()
				_, err := (&passiveQualityRepository{db: db}).advance(ctx, pid, aid, base.Add(14*time.Second), false)
				errs <- err
			}(pid, aid)
		}
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		require.NoError(t, err)
	}
	states, err := r.ListStates(ctx, 0)
	require.NoError(t, err)
	reduced := map[int64]bool{}
	guarded := false
	for _, s := range states {
		if s.Cache["model"].Phase == "reduced" {
			reduced[s.AccountID] = true
		}
		guarded = guarded || s.Cache["model"].Reason == "cache_pool_guard"
	}
	require.Len(t, reduced, 1)
	require.True(t, guarded)
}

func TestPassiveQualityCacheMissingCannotBlockAvailabilityRecoveryIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := v2CachePolicy()
	p.ErrorRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 1, Phase: "trial", TrialStartedAt: now.Add(-time.Second), TrialEndsAt: now.Add(time.Minute), ObserveAfter: p.UpdatedAt, UpdatedAt: now, Cache: map[string]service.PassiveQualityCacheState{"model": {Phase: "reduced", AllocationPercent: 25}}}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, savePassiveState(ctx, tx, s))
	require.NoError(t, tx.Commit())
	for i := 0; i < 5; i++ {
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms)VALUES(62,$1,$2,50)`, fmt.Sprintf("availability-%d", i), now)
		require.NoError(t, err)
	}
	_, err = r.advance(ctx, p.ID, 62, now.Add(time.Second), false)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "healthy", states[0].Phase)
	require.Equal(t, "reduced", states[0].Cache["model"].Phase)
}

func TestPassiveQualityV2ReplayOldHistoryUnavailableIntegration(t *testing.T) {
	ctx, db, r, _ := qualityOperationsDB(t)
	p := v2CachePolicy()
	from := time.Now().UTC().Add(-time.Hour)
	for i := 0; i < 10; i++ {
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,cache_read_tokens)VALUES(62,$1,$2,0)`, fmt.Sprintf("old-%d", i), from.Add(-time.Second))
		require.NoError(t, err)
	}
	result, err := r.ReplayQualityPolicy(ctx, service.QualityReplayRequest{Config: p.PassiveQualityConfig, AccountIDs: []int64{62}, From: from, To: from.Add(time.Minute), StepSeconds: 15})
	require.NoError(t, err)
	require.Contains(t, result.Warnings, "cache_reuse_history_unavailable")
	require.Zero(t, result.Triggers)
	for _, point := range result.Points {
		require.Equal(t, "pending", point.Verdict)
		require.NotEqual(t, "reduced", point.Cache["model"].Phase)
	}
	var n int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM passive_quality_states`).Scan(&n))
	require.Zero(t, n)
}
