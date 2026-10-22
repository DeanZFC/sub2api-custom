//go:build integration

package repository

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityAllModelsEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.Model, p.MaxSamples = "", 5
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.Equal(t, "account", p.Scope)
	now := time.Now()
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,model,requested_model,created_at,cache_read_tokens,first_token_ms) VALUES
 (62,'a','upstream-a','public-a',$1,100,10),
 (62,'b','b',NULL,$1,100,20),
 (62,'unknown-cache','c',NULL,$1,0,30),
 (62,'retry','b',NULL,$1,100,40),
 (63,'other-account','b',NULL,$1,100,50),
 (62,'old','old',NULL,$1-INTERVAL '1 second',100,9999)`, now)
	require.NoError(t, err)
	for _, key := range []string{"retry", "failure"} {
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "b", RequestKey: key, StartedAt: now, At: now.Add(time.Millisecond)}}, nil))
	}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	s := service.PassiveQualityState{AccountID: 62}
	e, err := passiveSamples(ctx, tx, p, s, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 5, e.Samples, "one bounded window across models, with failure winning a retry")
	require.Equal(t, 2, e.Failures)
	require.Equal(t, 40.0, *e.ErrorRate)
	require.Equal(t, 3, e.TTFTSamples)
	require.Equal(t, 30.0, *e.TTFT)
	require.Equal(t, 2, e.CacheSamples, "missing cache counters stay unknown")
	require.Equal(t, 50.0, *e.CacheRate)
	require.Equal(t, "b", e.ProbeModel)
	p.Model = "public-a"
	e, err = passiveSamples(ctx, tx, p, s, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, e.Samples)
	require.Zero(t, e.Failures)
	require.Equal(t, "public-a", e.ProbeModel)
	p.Model = "upstream-a"
	e, err = passiveSamples(ctx, tx, p, s, now.Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, e.Samples, "explicit filters still use the public requested model")
}

func TestPassiveQualityAllModelsLifecycleIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.Model, p.Confirmations, p.NewSamples, p.TrialRequests = "", 1, 1, 5
	require.NoError(t, r.SavePolicy(ctx, &p))
	for i := 0; i < 5; i++ {
		now := time.Now()
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: fmt.Sprintf("model-%d", i%2), RequestKey: fmt.Sprintf("failed-%d", i), StartedAt: now, At: now}}, nil))
	}
	_, err := r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	check := func(phase string) service.PassiveQualityState {
		t.Helper()
		states, err := r.ListStates(ctx, 62)
		require.NoError(t, err)
		require.Len(t, states, 1)
		require.Equal(t, phase, states[0].Phase)
		return states[0]
	}
	s := check("paused")
	require.Empty(t, s.Model)
	require.Equal(t, "model-0", s.ProbeModel)
	for _, model := range []string{"model-0", "model-1", "new-model", ""} {
		ok, err := acquirePassiveForTest(r, ctx, 62, model, "denied")
		require.NoError(t, err)
		require.False(t, ok)
	}
	require.NoError(t, r.Act(ctx, p.ID, 62, "verify", 0))
	probe, err := r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	require.NotNil(t, probe)
	require.Equal(t, "model-0", probe.ProbeModel)
	require.NoError(t, r.FinishProbe(ctx, *probe, true, time.Now()))
	_, err = r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	require.Equal(t, probe.ProbeModel, check("trial").ProbeModel, "probe success without fresh traffic cannot recover or lose the probe model")
	ok, err := acquirePassiveForTest(r, ctx, 62, "", "unknown-model")
	require.NoError(t, err)
	require.False(t, ok)
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := acquirePassiveForTest(r, ctx, 62, fmt.Sprintf("model-%d", i%2), fmt.Sprintf("mixed-%d", i))
			if err != nil {
				t.Errorf("admission: %v", err)
			}
			if ok {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(1), admitted.Load(), "concurrency is shared across models and connections")
	for i := 0; i < 12; i++ {
		require.NoError(t, r.ReleasePassiveQuality(ctx, fmt.Sprintf("mixed-%d", i), false))
	}
	for i := 1; i < 5; i++ {
		lease := fmt.Sprintf("serial-%d", i)
		ok, err := acquirePassiveForTest(r, ctx, 62, fmt.Sprintf("model-%d", i%2), lease)
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, r.ReleasePassiveQuality(ctx, lease, false))
	}
	ok, err = acquirePassiveForTest(r, ctx, 62, "new-model", "exhausted")
	require.NoError(t, err)
	require.False(t, ok, "changing models cannot reset the total trial budget")
	for i := 0; i < 5; i++ {
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,model,created_at) VALUES(62,$1,$2,$3)`, fmt.Sprintf("fresh-%d", i), fmt.Sprintf("model-%d", i%2), time.Now())
		require.NoError(t, err)
	}
	_, err = r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	require.Equal(t, 5, check("healthy").Evidence.Samples)
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=62`).Scan(&raw))
	var extra map[string]any
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.NotContains(t, extra, "passive_quality")
}

func TestPassiveQualityAllModelsLimitIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.Model, p.Action, p.ConcurrencyLimit = "", "limit", 2
	p.MaxSamples, p.Confirmations, p.NewSamples = 5, 1, 1
	require.NoError(t, r.SavePolicy(ctx, &p))
	for i := 0; i < 5; i++ {
		now := time.Now()
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: fmt.Sprintf("model-%d", i%2), RequestKey: fmt.Sprintf("failure-%d", i), StartedAt: now, At: now}}, nil))
	}
	_, err := r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	for _, model := range []string{"model-0", "model-1", "new-model"} {
		limit, err := r.PassiveQualityConcurrencyLimit(ctx, 62, model)
		require.NoError(t, err)
		require.Equal(t, 2, limit)
	}
	for i := 0; i < 5; i++ {
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,model,created_at) VALUES(62,$1,$2,$3)`, fmt.Sprintf("fresh-%d", i), fmt.Sprintf("model-%d", i%2), time.Now())
		require.NoError(t, err)
	}
	_, err = r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	limit, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "model-1")
	require.NoError(t, err)
	require.Zero(t, limit)
}

func TestPassiveQualityAllModelsOverlappingTrialIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	all, filtered := newPassivePolicy(), newPassivePolicy()
	all.Model, filtered.Model, filtered.Scope = "", "A", "account"
	for _, p := range []*service.PassiveQualityPolicy{&all, &filtered} {
		require.NoError(t, r.SavePolicy(ctx, p))
		now := time.Now()
		s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 1, Phase: "trial", TrialStartedAt: now, TrialEndsAt: now.Add(time.Minute)}
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, savePassiveState(ctx, tx, s))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
		require.NoError(t, tx.Commit())
	}
	ok, err := acquirePassiveForTest(r, ctx, 62, "B", "unrelated")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = acquirePassiveForTest(r, ctx, 62, "A", "matching")
	require.NoError(t, err)
	require.True(t, ok)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, states, 2)
	for _, s := range states {
		require.Equal(t, 1, s.Used)
	}
	require.NoError(t, r.ReleasePassiveQuality(ctx, "matching", true))
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	for _, s := range states {
		require.Zero(t, s.Used)
	}
}

func TestPassiveQualityAllModelsProbeRetentionIntegration(t *testing.T) {
	for _, model := range []string{"", "observed-model"} {
		t.Run("probe="+model, func(t *testing.T) {
			ctx, db, r := passiveDB(t)
			p := newPassivePolicy()
			p.Model = ""
			require.NoError(t, r.SavePolicy(ctx, &p))
			now := time.Now()
			s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 1, Phase: "trial", ProbeModel: model, TrialStartedAt: now.Add(-time.Minute), TrialEndsAt: now.Add(-time.Second)}
			tx, err := db.BeginTx(ctx, nil)
			require.NoError(t, err)
			require.NoError(t, savePassiveState(ctx, tx, s))
			require.NoError(t, tx.Commit())
			_, err = r.advance(ctx, p.ID, 62, now, true)
			require.NoError(t, err)
			states, err := r.ListStates(ctx, 62)
			require.NoError(t, err)
			require.Equal(t, "paused", states[0].Phase)
			probe, err := r.advance(ctx, p.ID, 62, states[0].NextAt.Add(time.Second), true)
			require.NoError(t, err)
			if model == "" {
				require.Nil(t, probe, "never send a platform default model without observed evidence")
				states, err = r.ListStates(ctx, 62)
				require.NoError(t, err)
				require.Equal(t, "paused", states[0].Phase)
				require.Equal(t, "probe_model_unavailable", states[0].Reason)
			} else {
				require.NotNil(t, probe)
				require.Equal(t, model, probe.ProbeModel, "empty trials must retain the previous probe target")
				require.NoError(t, r.SavePolicy(ctx, &p))
				require.NoError(t, r.FinishProbe(ctx, *probe, true, now))
				states, err = r.ListStates(ctx, 62)
				require.NoError(t, err)
				require.Empty(t, states, "a stale probe cannot recreate a hold or trial after policy edits")
			}
		})
	}
}
