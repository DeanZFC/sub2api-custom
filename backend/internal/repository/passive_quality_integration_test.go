//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func passiveDB(t *testing.T) (context.Context, *sql.DB, *passiveQualityRepository) {
	t.Helper()
	dsn := os.Getenv("MIGRATION_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("MIGRATION_TEST_DATABASE_URL is not set")
	}
	ctx := context.Background()
	admin, err := sql.Open("postgres", dsn)
	require.NoError(t, err)
	schema := fmt.Sprintf("passive_%d", time.Now().UnixNano())
	_, err = admin.Exec(`CREATE SCHEMA ` + pq.QuoteIdentifier(schema))
	require.NoError(t, err)
	u, err := url.Parse(dsn)
	require.NoError(t, err)
	q := u.Query()
	q.Set("search_path", schema)
	u.RawQuery = q.Encode()
	db, err := sql.Open("postgres", u.String())
	require.NoError(t, err)
	t.Cleanup(func() {
		db.Close()
		_, err := admin.Exec(`DROP SCHEMA ` + pq.QuoteIdentifier(schema) + ` CASCADE`)
		require.NoError(t, err)
		admin.Close()
	})
	exec := func(q string) { t.Helper(); _, err := db.Exec(q); require.NoError(t, err) }
	exec(`CREATE TABLE accounts(id BIGINT PRIMARY KEY,name TEXT,status TEXT DEFAULT 'active',schedulable BOOLEAN DEFAULT true,deleted_at TIMESTAMPTZ,extra JSONB DEFAULT '{}',updated_at TIMESTAMPTZ DEFAULT now(),temp_unschedulable_until TIMESTAMPTZ,rate_limit_reset_at TIMESTAMPTZ);
 CREATE TABLE groups(id BIGINT PRIMARY KEY,platform TEXT DEFAULT 'openai',status TEXT DEFAULT 'active',deleted_at TIMESTAMPTZ);
 CREATE TABLE account_groups(account_id BIGINT,group_id BIGINT);
 CREATE TABLE scheduler_outbox(id BIGSERIAL PRIMARY KEY,event_type TEXT,account_id BIGINT,group_id BIGINT,payload JSONB,dedup_key TEXT,created_at TIMESTAMPTZ DEFAULT now());
 CREATE UNIQUE INDEX outbox_dedup ON scheduler_outbox(dedup_key) WHERE dedup_key IS NOT NULL;
 CREATE TABLE usage_logs(id BIGSERIAL PRIMARY KEY,account_id BIGINT,request_id TEXT,created_at TIMESTAMPTZ,model TEXT DEFAULT 'model',requested_model TEXT,duration_ms BIGINT DEFAULT 0,request_type SMALLINT DEFAULT 2,stream BOOLEAN DEFAULT true,openai_ws_mode BOOLEAN DEFAULT false,first_token_ms BIGINT,input_tokens BIGINT DEFAULT 100,output_tokens BIGINT DEFAULT 1,cache_creation_tokens BIGINT DEFAULT 0,cache_read_tokens BIGINT DEFAULT 0,image_input_tokens BIGINT DEFAULT 0,image_output_tokens BIGINT DEFAULT 0,image_count INT DEFAULT 0,video_count INT DEFAULT 0,actual_cost NUMERIC DEFAULT 0,total_cost NUMERIC DEFAULT 0,native_compaction_v2 BOOLEAN DEFAULT false,inbound_endpoint TEXT,upstream_endpoint TEXT);
 INSERT INTO accounts(id,name) VALUES(62,'A'),(63,'B');INSERT INTO groups(id)VALUES(8),(9);INSERT INTO account_groups VALUES(62,8),(63,8);`)
	migration, err := os.ReadFile("../../migrations/291_passive_quality.sql")
	require.NoError(t, err)
	exec(string(migration))
	migration, err = os.ReadFile("../../migrations/295_passive_quality_account_window.sql")
	require.NoError(t, err)
	exec(string(migration))
	return ctx, db, &passiveQualityRepository{db: db}
}
func newPassivePolicy() service.PassiveQualityPolicy {
	return service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Name: "quality", Enabled: true, Mode: "enforce", GroupIDs: []int64{8}, ExcludedAccountIDs: []int64{}, Model: "model", Scope: "model", WindowSeconds: 600, MaxSamples: 100, MinSamples: 5, Confirmations: 2, NewSamples: 2, ErrorRate: service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 5}, CooldownSeconds: 30, MaxCooldownSeconds: 300, TrialSeconds: 60, TrialRequests: 10, TrialConcurrency: 1, RecoverySamples: 5}}
}
func TestPassiveQualityLifecycleIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	insertFailure := func(i int, at time.Time) {
		t.Helper()
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: fmt.Sprintf("client:r%d", i), StartedAt: at, At: at}}, nil))
	}
	for i := 0; i < 5; i++ {
		insertFailure(i, now.Add(time.Duration(i)*time.Millisecond))
	}
	check := func(want string) service.PassiveQualityState {
		t.Helper()
		states, err := r.ListStates(ctx, 62)
		require.NoError(t, err)
		require.Len(t, states, 1)
		require.Equal(t, want, states[0].Phase)
		return states[0]
	}
	_, err := r.advance(ctx, p.ID, 62, now.Add(time.Second), true)
	require.NoError(t, err)
	s := check("suspect")
	require.Equal(t, 1, s.Hits)
	// Re-reading the same window must not trigger confirmation.
	_, err = r.advance(ctx, p.ID, 62, now.Add(2*time.Second), true)
	require.NoError(t, err)
	require.Equal(t, 1, check("suspect").Hits)
	for i := 5; i < 7; i++ {
		insertFailure(i, now.Add(3*time.Second+time.Duration(i)*time.Millisecond))
	}
	_, err = r.advance(ctx, p.ID, 62, now.Add(4*time.Second), true)
	require.NoError(t, err)
	s = check("paused")
	var status string
	var sched bool
	require.NoError(t, db.QueryRow(`SELECT status,schedulable FROM accounts WHERE id=62`).Scan(&status, &sched))
	require.Equal(t, "active", status)
	require.True(t, sched)
	ok, err := acquirePassiveForTest(r, ctx, 62, "model", "a")
	require.NoError(t, err)
	require.False(t, ok)
	ok, err = acquirePassiveForTest(r, ctx, 62, "other", "b")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, r.Act(ctx, p.ID, 62, "verify", 0))
	probe, err := r.advance(ctx, p.ID, 62, time.Now().Add(time.Millisecond), true)
	require.NoError(t, err)
	require.NotNil(t, probe)
	require.NoError(t, r.FinishProbe(ctx, *probe, true, time.Now()))
	s = check("trial")
	require.Zero(t, s.Used)
	// Cross-instance reservations cannot exceed concurrency=1.
	var admitted atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 12; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			ok, err := acquirePassiveForTest(r, ctx, 62, "model", fmt.Sprintf("lease%d", i))
			if err != nil {
				t.Errorf("admission: %v", err)
			}
			if ok {
				admitted.Add(1)
			}
		}(i)
	}
	wg.Wait()
	require.Equal(t, int32(1), admitted.Load())
	// A successful probe with no fresh usage remains in trial.
	_, err = r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	check("trial")
	for i := 0; i < 12; i++ {
		require.NoError(t, r.ReleasePassiveQuality(ctx, fmt.Sprintf("lease%d", i), false))
	}
	for i := 0; i < 5; i++ {
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms)VALUES(62,$1,$2,50)`, fmt.Sprintf("fresh%d", i), time.Now())
		require.NoError(t, err)
	}
	_, err = r.advance(ctx, p.ID, 62, time.Now(), true)
	require.NoError(t, err)
	check("healthy")
	var extra map[string]any
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=62`).Scan(&raw))
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.NotContains(t, extra, "passive_quality")
	recovered := check("healthy")
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: "late-trial-failure", StartedAt: s.TrialStartedAt, At: time.Now()}}, nil))
	readTx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	late, err := passiveSamples(ctx, readTx, p, recovered, time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, late.Failures)
	require.NoError(t, readTx.Rollback())
	events, err := r.ListEvents(ctx, 62, 0)
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(events), 5)
}
func TestPassiveQualityEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	// A failed A attempt followed by success on B is an A failure and B success.
	failures := []service.PassiveQualityFailure{{AccountID: 62, Model: "model", RequestKey: "client:retry", StartedAt: now, At: now}, {AccountID: 62, Model: "model", RequestKey: "client:retry", StartedAt: now, At: now}}
	require.NoError(t, r.RecordFailures(ctx, failures, nil))
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,cache_read_tokens,first_token_ms)VALUES(62,'client:retry',$1,100,10),(63,'client:retry',$1,100,10),(62,'free',$1,100,40),(62,'missing',$1,0,NULL)`, now)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	s := service.PassiveQualityState{AccountID: 62, ObserveAfter: p.UpdatedAt}
	e, err := passiveSamples(ctx, tx, p, s, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 3, e.Samples)
	require.Equal(t, 1, e.Failures)
	require.Equal(t, 1, e.TTFTSamples)
	s.AccountID = 63
	e, err = passiveSamples(ctx, tx, p, s, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 1, e.Samples)
	require.Zero(t, e.Failures)
	s.AccountID = 62
	s.Phase = "trial"
	s.TrialStartedAt = now.Add(time.Millisecond)
	e, err = passiveSamples(ctx, tx, p, s, now.Add(time.Second))
	require.NoError(t, err)
	require.Zero(t, e.Samples)
}

func TestPassiveQualityWebSocketTurnEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{
		{AccountID: 62, Model: "model", RequestKey: "ws:failed-turn", StartedAt: now, At: now},
		{AccountID: 62, RequestKey: "resp_failed", AttemptKey: "ws:failed-turn", At: now},
		{AccountID: 62, RequestKey: "resp_unknown", AttemptKey: "ws:unknown-turn", At: now},
		{AccountID: 62, RequestKey: "resp_zero", AttemptKey: "ws:zero-turn", CacheReported: true, At: now},
	}, nil))
	_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,openai_ws_mode,first_token_ms) VALUES(62,'resp_failed',$1,true,50),(62,'resp_unknown',$1,true,50),(62,'resp_zero',$1,true,50)`, now)
	require.NoError(t, err)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	e, err := passiveSamples(ctx, tx, p, service.PassiveQualityState{AccountID: 62, ObserveAfter: p.UpdatedAt}, now.Add(time.Second))
	require.NoError(t, err)
	require.Equal(t, 3, e.Samples, "one failed turn must not also count as a successful usage row")
	require.Equal(t, 1, e.Failures)
	require.Equal(t, 2, e.TTFTSamples)
	require.Equal(t, 1, e.CacheSamples, "turn correlation without reported cache usage is still unknown")
	require.NotNil(t, e.CacheRate)
	require.Zero(t, *e.CacheRate)
}
func TestPassiveQualityOwnershipAndFencingIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 3, Phase: "probing", NextAt: now.Add(time.Minute)}
	persist := func() {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, savePassiveState(ctx, tx, s))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
		require.NoError(t, tx.Commit())
	}
	persist()
	require.NoError(t, r.Act(ctx, p.ID, 62, "exempt", 60))
	require.NoError(t, r.FinishProbe(ctx, s, true, now))
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "exempt", states[0].Phase)
	_, err = db.Exec(`UPDATE accounts SET schedulable=false,status='quality_paused',extra=extra||'{"quality_protection_reason":"active hold"}' WHERE id=62`)
	require.NoError(t, err)
	require.NoError(t, r.DeletePolicy(ctx, p.ID))
	var sched bool
	var status, reason string
	require.NoError(t, db.QueryRow(`SELECT schedulable,status,extra->>'quality_protection_reason' FROM accounts WHERE id=62`).Scan(&sched, &status, &reason))
	require.False(t, sched)
	require.Equal(t, "quality_paused", status)
	require.Equal(t, "active hold", reason)
	p = newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	stale := p
	p.Name = "edited"
	require.NoError(t, r.SavePolicy(ctx, &p))
	require.ErrorIs(t, r.SavePolicy(ctx, &stale), service.ErrPassiveQualityConflict)
	s.PolicyID = p.ID
	s.Version = p.Version
	s.Phase = "paused"
	persist()
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	_, err = tx.Exec(`UPDATE account_groups SET group_id=9 WHERE account_id=62`)
	require.NoError(t, err)
	require.NoError(t, releaseDetachedPassiveProtection(ctx, tx, 62))
	require.NoError(t, tx.Commit())
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Empty(t, states)
}
func TestPassiveQualityTrialTimeoutAndBudgetIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.TrialRequests = 5
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 3, Phase: "trial", TrialStartedAt: now.Add(-time.Second), TrialEndsAt: now.Add(time.Minute)}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, savePassiveState(ctx, tx, s))
	require.NoError(t, tx.Commit())
	for i := 0; i < 5; i++ {
		ok, err := acquirePassiveForTest(r, ctx, 62, "model", "lease")
		require.NoError(t, err)
		require.True(t, ok)
		require.NoError(t, r.ReleasePassiveQuality(ctx, "lease", false))
	}
	ok, err := acquirePassiveForTest(r, ctx, 62, "model", "extra")
	require.NoError(t, err)
	require.False(t, ok)
	_, err = r.advance(ctx, p.ID, 62, now.Add(61*time.Second), true)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "paused", states[0].Phase)
	require.Contains(t, states[0].Reason, "trial_expired")
}

func TestPassiveQualitySweepObservationAndCacheSamplesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.Mode = "observe"
	p.Confirmations = 1
	p.ExcludedAccountIDs = []int64{63}
	p.ErrorRate.Enabled = false
	p.CacheRate = service.PassiveQualityMetric{Enabled: true, Pause: 10, Recover: 20}
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	for i := 0; i < 6; i++ {
		key := fmt.Sprintf("zero-cache%d", i)
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at)VALUES(62,$1,$2)`, key, now)
		require.NoError(t, err)
	}
	_, err := r.Sweep(ctx, now.Add(time.Second), 4)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 0)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, "insufficient", states[0].Phase)
	require.Zero(t, states[0].Evidence.CacheSamples)
	// An explicitly reported zero cache counter is a valid sample. Omitted is not.
	for i := 0; i < 6; i++ {
		key := fmt.Sprintf("reported-zero%d", i)
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at)VALUES(62,$1,$2)`, key, now.Add(2*time.Second))
		require.NoError(t, err)
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, RequestKey: key, At: now, CacheReported: true}}, nil))
	}
	_, err = r.Sweep(ctx, now.Add(3*time.Second), 4)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "observed", states[0].Phase)
	require.Equal(t, 6, states[0].Evidence.CacheSamples)
	ok, err := acquirePassiveForTest(r, ctx, 62, "model", "observe")
	require.NoError(t, err)
	require.True(t, ok)
	gap := now.Add(4 * time.Second)
	require.NoError(t, r.RecordFailures(ctx, nil, &gap))
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	e, err := passiveSamples(ctx, tx, p, states[0], now.Add(5*time.Second))
	require.NoError(t, err)
	require.False(t, e.Complete)
}

func TestPassiveQualityReservationRefundIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 1, Phase: "trial", TrialStartedAt: now.Add(-time.Second), TrialEndsAt: now.Add(time.Minute)}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, savePassiveState(ctx, tx, s))
	require.NoError(t, tx.Commit())
	ok, err := acquirePassiveForTest(r, ctx, 62, "model", "denied-by-other-gate")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, r.ReleasePassiveQuality(ctx, "denied-by-other-gate", true))
	require.NoError(t, r.ReleasePassiveQuality(ctx, "denied-by-other-gate", true))
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Zero(t, states[0].Used)
	require.Zero(t, states[0].Inflight)
}

func acquirePassiveForTest(r *passiveQualityRepository, ctx context.Context, aid int64, model, lease string) (bool, error) {
	ok, _, err := r.AcquirePassiveQuality(ctx, aid, model, lease)
	return ok, err
}

func TestPassiveQualityIndependentAccountHoldsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	a, b := newPassivePolicy(), newPassivePolicy()
	a.Scope = "account"
	b.Scope = "account"
	a.Model = "A"
	b.Model = "B"
	require.NoError(t, r.SavePolicy(ctx, &a))
	require.NoError(t, r.SavePolicy(ctx, &b))
	now := time.Now()
	states := []service.PassiveQualityState{}
	for _, p := range []service.PassiveQualityPolicy{a, b} {
		states = append(states, service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, Version: p.Version, AccountID: 62, Mode: p.Mode, Scope: p.Scope, Model: p.Model, Generation: 1, Phase: "trial", TrialStartedAt: now.Add(-time.Second), TrialEndsAt: now.Add(time.Minute)})
	}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	for _, s := range states {
		require.NoError(t, savePassiveState(ctx, tx, s))
	}
	require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
	require.NoError(t, tx.Commit())
	ok, err := acquirePassiveForTest(r, ctx, 62, "A", "only-a")
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, r.ReleasePassiveQuality(ctx, "only-a", false))
	current, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, 1, current[0].Used)
	require.Zero(t, current[1].Used)
	states[1].Phase = "paused"
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, savePassiveState(ctx, tx, states[1]))
	require.NoError(t, tx.Commit())
	ok, err = acquirePassiveForTest(r, ctx, 62, "A", "other-blocks")
	require.NoError(t, err)
	require.False(t, ok)
	require.NoError(t, r.DeletePolicy(ctx, a.ID))
	var raw []byte
	require.NoError(t, db.QueryRow(`SELECT extra->'passive_quality' FROM accounts WHERE id=62`).Scan(&raw))
	var holds []map[string]any
	require.NoError(t, json.Unmarshal(raw, &holds))
	require.Len(t, holds, 1)
	require.Equal(t, float64(b.ID), holds[0]["policy_id"])
}

func TestPassiveQualityLimitedRecoveryAndIndependentHolds(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.Action = "limit"
	p.ConcurrencyLimit = 2
	p.PriorityPenalty = 10
	p.MaxSamples = 5
	p.Confirmations = 1
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	failures := []service.PassiveQualityFailure{}
	for i := 0; i < 5; i++ {
		at := now.Add(time.Duration(i) * time.Millisecond)
		failures = append(failures, service.PassiveQualityFailure{AccountID: 62, Model: "model", RequestKey: fmt.Sprint(i), StartedAt: at, At: at})
	}
	require.NoError(t, r.RecordFailures(ctx, failures, nil))
	_, err := r.advance(ctx, p.ID, 62, now.Add(time.Second), true)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "limited", states[0].Phase)
	cap, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Equal(t, 2, cap)
	cap, err = r.PassiveQualityConcurrencyLimit(ctx, 62, "other")
	require.NoError(t, err)
	require.Zero(t, cap)
	ok, err := acquirePassiveForTest(r, ctx, 62, "model", "limited")
	require.NoError(t, err)
	require.True(t, ok)
	// Keep another owner paused while this policy recovers.
	_, err = db.Exec(`UPDATE accounts SET status='quality_paused',schedulable=false WHERE id=62`)
	require.NoError(t, err)
	for i := 0; i < 5; i++ {
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms)VALUES(62,$1,$2,50)`, fmt.Sprintf("fresh%d", i), now.Add(2*time.Second+time.Duration(i)*time.Millisecond))
		require.NoError(t, err)
	}
	_, err = r.advance(ctx, p.ID, 62, now.Add(3*time.Second), true)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "healthy", states[0].Phase)
	cap, err = r.PassiveQualityConcurrencyLimit(ctx, 62, "model")
	require.NoError(t, err)
	require.Zero(t, cap)
	var status string
	var sched bool
	require.NoError(t, db.QueryRow(`SELECT status,schedulable FROM accounts WHERE id=62`).Scan(&status, &sched))
	require.Equal(t, "quality_paused", status)
	require.False(t, sched)
}

func TestPassiveQualityLimitedRequiresFreshRecoveryConfirmations(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.Action = "limit"
	p.ConcurrencyLimit = 1
	p.MaxSamples = 5
	p.Confirmations = 2
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	for step := 0; step < 2; step++ {
		failures := []service.PassiveQualityFailure{}
		for i := 0; i < 5; i++ {
			at := now.Add(time.Duration(step*10+i) * time.Millisecond)
			failures = append(failures, service.PassiveQualityFailure{AccountID: 62, Model: "model", RequestKey: fmt.Sprintf("%d-%d", step, i), StartedAt: at, At: at})
		}
		require.NoError(t, r.RecordFailures(ctx, failures, nil))
		_, err := r.advance(ctx, p.ID, 62, now.Add(time.Duration(step+1)*time.Second), true)
		require.NoError(t, err)
	}
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "limited", states[0].Phase)
	for i := 0; i < 5; i++ {
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms)VALUES(62,$1,$2,50)`, fmt.Sprint(i), now.Add(3*time.Second+time.Duration(i)*time.Millisecond))
		require.NoError(t, err)
	}
	for step := 0; step < 3; step++ {
		_, err = r.advance(ctx, p.ID, 62, now.Add(4*time.Second), true)
		require.NoError(t, err)
	}
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "limited", states[0].Phase)
	require.Equal(t, 1, states[0].Hits)
	for i := 0; i < 2; i++ {
		_, err = db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,first_token_ms)VALUES(62,$1,$2,50)`, fmt.Sprintf("new%d", i), now.Add(5*time.Second+time.Duration(i)*time.Millisecond))
		require.NoError(t, err)
	}
	_, err = r.advance(ctx, p.ID, 62, now.Add(6*time.Second), true)
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "healthy", states[0].Phase)
}
