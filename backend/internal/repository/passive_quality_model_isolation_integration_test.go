//go:build integration

package repository

import (
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestPassiveQualityModelMigrationPreservesExistingLeaseIntegration(t *testing.T) {
	ctx, db, r := passiveDBSchema(t, false)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: p.Model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 7, Phase: "trial", Used: 1, TrialStartedAt: now, TrialEndsAt: now.Add(time.Minute)}
	raw, err := json.Marshal(s)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_states(policy_id,account_id,data)VALUES($1,62,$2)`, p.ID, raw)
	require.NoError(t, err)
	_, err = db.Exec(`INSERT INTO passive_quality_leases(id,policy_id,account_id,generation)VALUES('old', $1,62,7)`, p.ID)
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/303_passive_quality_model_states.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	var key string
	require.NoError(t, db.QueryRow(`SELECT model_key FROM passive_quality_states`).Scan(&key))
	require.Equal(t, "model", key)
	require.NoError(t, db.QueryRow(`SELECT model_key FROM passive_quality_leases`).Scan(&key))
	require.Equal(t, "model", key)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, states, 1)
	require.Equal(t, "trial", states[0].Phase)
	require.EqualValues(t, 7, states[0].Generation)
	require.Equal(t, 1, states[0].Inflight)
	allowed, _, err := r.AcquirePassiveQuality(ctx, 62, "model", "new")
	require.NoError(t, err)
	require.False(t, allowed)
	require.NoError(t, r.ReleasePassiveQuality(ctx, "old", true))
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Zero(t, states[0].Used)
	require.Zero(t, states[0].Inflight)
}

func TestPassiveQualityModelDiscoveryAndProtectionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.Model = ""
	p.Scope = "model"
	p.Confirmations = 1
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "A", RequestKey: fmt.Sprintf("bad-%d", i), StartedAt: at, At: at}, {AccountID: 62, Model: "", RequestKey: fmt.Sprintf("unknown-%d", i), StartedAt: at, At: at}}, nil))
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model,requested_model)VALUES(62,$1,$2,'upstream-B','B')`, fmt.Sprintf("ok-%d", i), at)
		require.NoError(t, err)
	}
	_, err := r.Sweep(ctx, base.Add(time.Second), 0)
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Len(t, states, 2)
	byModel := map[string]service.PassiveQualityState{}
	for _, s := range states {
		byModel[s.Model] = s
	}
	require.Equal(t, "paused", byModel["A"].Phase)
	require.Equal(t, "healthy", byModel["B"].Phase)
	require.Equal(t, 5, byModel["A"].Evidence.Samples)
	require.Equal(t, 5, byModel["B"].Evidence.Samples)
	allowed, _, err := r.AcquirePassiveQuality(ctx, 62, "A", "blocked")
	require.NoError(t, err)
	require.False(t, allowed)
	allowed, leased, err := r.AcquirePassiveQuality(ctx, 62, "B", "unrelated")
	require.NoError(t, err)
	require.True(t, allowed)
	require.False(t, leased)
	var extra []byte
	require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=62`).Scan(&extra))
	var a service.Account
	require.NoError(t, json.Unmarshal(extra, &a.Extra))
	require.True(t, a.PassiveQualityBlocked("A"))
	require.False(t, a.PassiveQualityBlocked("B"))
	probes, err := r.Sweep(ctx, base.Add(2*time.Hour), 4)
	require.NoError(t, err)
	require.Len(t, probes, 1)
	require.Equal(t, "A", probes[0].Model)
	require.Equal(t, "A", probes[0].ProbeModel)
}

func TestPassiveQualityModelTrialLeaseAndActionsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.Model = ""
	p.Scope = "model"
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	probes := map[string]service.PassiveQualityState{}
	for _, model := range []string{"A", "B"} {
		s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: model, ProbeModel: model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 3, Phase: "probing", NextAt: now.Add(time.Minute), UpdatedAt: now}
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, savePassiveState(ctx, tx, s))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
		require.NoError(t, tx.Commit())
		probes[model] = s
	}
	require.NoError(t, r.PostponePassiveProbe(ctx, probes["A"], now.Add(time.Minute)))
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "paused", states[0].Phase)
	require.Equal(t, "probing", states[1].Phase)
	// Restore A probe with its current generation; B completion cannot change A.
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	a := states[0]
	a.Phase = "probing"
	require.NoError(t, savePassiveState(ctx, tx, a))
	require.NoError(t, tx.Commit())
	require.NoError(t, r.FinishProbe(ctx, probes["B"], true, now))
	require.NoError(t, r.FinishProbe(ctx, a, true, now))
	allowed, leased, err := r.AcquirePassiveQuality(ctx, 62, "A", "lease-a")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, leased)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, 1, states[0].Used)
	require.Equal(t, 1, states[0].Inflight)
	require.Zero(t, states[1].Used)
	require.Zero(t, states[1].Inflight)
	allowed, leased, err = r.AcquirePassiveQuality(ctx, 62, "B", "lease-b")
	require.NoError(t, err)
	require.True(t, allowed)
	require.True(t, leased)
	require.ErrorIs(t, r.Act(ctx, p.ID, 62, "exempt", 60), service.ErrPassiveQualityModelRequired)
	require.NoError(t, r.Act(ctx, p.ID, 62, "exempt", 60, "A"))
	require.NoError(t, r.FinishProbe(ctx, probes["A"], true, now))
	require.NoError(t, r.ReleasePassiveQuality(ctx, "lease-a", true))
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "exempt", states[0].Phase)
	require.Equal(t, "trial", states[1].Phase)
	require.Equal(t, 1, states[1].Used)
	require.Equal(t, 1, states[1].Inflight)
	require.NoError(t, r.ReleasePassiveQuality(ctx, "lease-b", true))
	require.NoError(t, r.Act(ctx, p.ID, 62, "cancel_exemption", 0, "A"))
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "insufficient", states[0].Phase)
	require.Equal(t, "trial", states[1].Phase)
	require.Zero(t, states[1].Used)
	// Replacing the policy clears every model and every lease, not just one row.
	require.NoError(t, r.SavePolicy(ctx, &p))
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Empty(t, states)
}

func TestPassiveQualityModelLimitAndReplayIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.Model = ""
	p.Scope = "model"
	p.Action = "limit"
	p.ConcurrencyLimit = 2
	p.PriorityPenalty = 10
	p.Confirmations = 1
	require.NoError(t, r.SavePolicy(ctx, &p))
	base := p.UpdatedAt.Add(time.Second)
	for i := 0; i < 5; i++ {
		at := base.Add(time.Duration(i) * time.Millisecond)
		require.NoError(t, r.RecordFailures(ctx, []service.PassiveQualityFailure{{AccountID: 62, Model: "A", RequestKey: fmt.Sprintf("bad-%d", i), StartedAt: at, At: at}}, nil))
		_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model)VALUES(62,$1,$2,'B')`, fmt.Sprintf("ok-%d", i), at)
		require.NoError(t, err)
	}
	_, err := r.Sweep(ctx, base.Add(time.Second), 0)
	require.NoError(t, err)
	limit, err := r.PassiveQualityConcurrencyLimit(ctx, 62, "A")
	require.NoError(t, err)
	require.Equal(t, 2, limit)
	limit, err = r.PassiveQualityConcurrencyLimit(ctx, 62, "B")
	require.NoError(t, err)
	require.Zero(t, limit)
	qr := qualityOperationsRepository{db: db}
	result, err := qr.ReplayQualityPolicy(ctx, service.QualityReplayRequest{Config: p.PassiveQualityConfig, AccountIDs: []int64{62}, From: base.Add(time.Second), To: base.Add(16 * time.Second), StepSeconds: 15})
	require.NoError(t, err)
	require.Len(t, result.Points, 4)
	require.Equal(t, 1, result.Triggers)
	for _, point := range result.Points {
		if point.Model == "B" {
			require.Equal(t, "pass", point.Verdict)
			require.Empty(t, point.Action)
		} else {
			require.Equal(t, "A", point.Model)
			require.Equal(t, "fail", point.Verdict)
		}
	}
}

func TestPassiveQualityModelRecoveryRequiresOwnEvidenceIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.Model = ""
	p.Scope = "model"
	require.NoError(t, r.SavePolicy(ctx, &p))
	now := time.Now()
	for _, model := range []string{"A", "B"} {
		s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: 62, Model: model, ProbeModel: model, Scope: p.Scope, Mode: p.Mode, Version: p.Version, Generation: 3, Phase: "trial", TrialStartedAt: now, TrialEndsAt: now.Add(time.Minute), UpdatedAt: now}
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		require.NoError(t, savePassiveState(ctx, tx, s))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, 62))
		require.NoError(t, tx.Commit())
	}
	seed := func(model string, at time.Time) {
		t.Helper()
		for i := 0; i < 5; i++ {
			_, err := db.Exec(`INSERT INTO usage_logs(account_id,request_id,created_at,model)VALUES(62,$1,$2,$3)`, fmt.Sprintf("%s-%d", model, i), at.Add(time.Duration(i)*time.Millisecond), model)
			require.NoError(t, err)
		}
	}
	seed("B", now.Add(time.Second))
	_, err := r.advance(ctx, p.ID, 62, now.Add(2*time.Second), false, "A")
	require.NoError(t, err)
	_, err = r.advance(ctx, p.ID, 62, now.Add(2*time.Second), false, "B")
	require.NoError(t, err)
	states, err := r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "trial", states[0].Phase)
	require.Equal(t, "healthy", states[1].Phase)
	seed("A", now.Add(3*time.Second))
	_, err = r.advance(ctx, p.ID, 62, now.Add(4*time.Second), false, "A")
	require.NoError(t, err)
	states, err = r.ListStates(ctx, 62)
	require.NoError(t, err)
	require.Equal(t, "healthy", states[0].Phase)
	require.Equal(t, "healthy", states[1].Phase)
}
