//go:build integration

package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	dbent "github.com/Wei-Shaw/sub2api/ent"
	"github.com/Wei-Shaw/sub2api/internal/pkg/ctxkey"
	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func recoveryAccountRepo(f *passRateFixture) *accountRepository {
	client := dbent.NewClient(dbent.Driver(entsql.OpenDB(dialect.Postgres, f.db)))
	return newAccountRepositoryWithSQL(client, f.db, nil)
}

func assertRecoveredQuality(t *testing.T, f *passRateFixture, status string, schedulable bool) {
	t.Helper()
	var actualStatus string
	var actualSchedulable bool
	var raw []byte
	require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT status,schedulable,extra FROM accounts WHERE id=62`).Scan(&actualStatus, &actualSchedulable, &raw))
	require.Equal(t, status, actualStatus)
	require.Equal(t, schedulable, actualSchedulable)
	var extra map[string]any
	require.NoError(t, json.Unmarshal(raw, &extra))
	require.Equal(t, "keep", extra["setting"])
	for _, key := range []string{"active_test_model_holds", "active_pass_rate_holds", "active_test_all_models_paused", "quality_protection_reason", "quality_protection_trial"} {
		require.NotContains(t, extra, key)
	}
	for _, table := range []string{"scheduled_test_protection_states", "scheduled_test_combination_states", "scheduled_test_pass_rate_states", "scheduled_test_all_pass_rounds"} {
		var count int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM `+table+` WHERE account_id=62`).Scan(&count))
		require.Zero(t, count, table)
	}
}

func TestAccountQualityRecoveryIntegration(t *testing.T) {
	t.Run("release all gates and fence old results but retain future protection", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("fail")
		f.result("fail")
		require.True(t, f.summary("model-a").Blocked)
		pending := f.begin()
		f.exec(`UPDATE scheduled_test_protection_states SET blocked=TRUE,reason='capability failed' WHERE account_id=62`)
		f.exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,model_key,blocked,reason) VALUES($1,62,'model-a',TRUE,'combined hold'),($1,63,'model-a',TRUE,'other account hold')`, f.plan.ID)
		f.exec(`INSERT INTO scheduled_test_all_pass_rounds(plan_id,account_id,model_key,run_id,started_at,rule_version) VALUES($1,62,'model-a',$2,NOW(),'test') ON CONFLICT DO NOTHING`, f.plan.ID, pending.RunID)
		f.exec(`UPDATE accounts SET extra=extra||'{"setting":"keep","quality_protection_trial":{"remaining":1}}' WHERE id=62`)
		repo := recoveryAccountRepo(f)
		ctx := context.WithValue(f.ctx, ctxkey.Model, "model-a")
		admitted, err := repo.CheckAccountAdmission(ctx, 62)
		require.ErrorIs(t, err, service.ErrAccountQualityHeld)
		require.False(t, admitted)
		evidence := f.evidenceCount()
		require.NoError(t, repo.ClearAccountQualityProtection(f.ctx, 62))
		assertRecoveredQuality(t, f, "active", true)
		admitted, err = repo.CheckAccountAdmission(ctx, 62)
		require.NoError(t, err)
		require.True(t, admitted, "actual database admission must reopen, not just the account status")
		require.Equal(t, evidence, f.evidenceCount(), "keep historical quality evidence")
		var otherHolds, history, events int
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM scheduled_test_combination_states WHERE account_id=63 AND blocked`).Scan(&otherHolds))
		require.Equal(t, 1, otherHolds)
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM scheduled_test_results WHERE account_id=62`).Scan(&history))
		require.Equal(t, 3, history)
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT count(*) FROM scheduler_outbox WHERE account_id=62 AND event_type='account_changed'`).Scan(&events))
		require.Positive(t, events)
		eligible, err := f.repo.IsPlanRunAccountEligible(f.ctx, f.plan.ID, pending.RunID, 62)
		require.NoError(t, err)
		require.False(t, eligible)
		require.NoError(t, f.repo.BeginProtectionRun(f.ctx, f.plan, pending.StartedAt))
		require.NoError(t, f.repo.BeginProtection(f.ctx, pending, f.plan.Protection.Rules[0]))
		f.complete(pending, "success", "fail")
		assertRecoveredQuality(t, f, "active", true)
		require.Equal(t, evidence, f.evidenceCount())
		require.NoError(t, repo.ClearAccountQualityProtection(f.ctx, 62), "repeated recovery is safe")
		f.result("fail")
		require.True(t, f.summary("model-a").Blocked, "new quality checks still enforce the unchanged policy")
		admitted, err = repo.CheckAccountAdmission(ctx, 62)
		require.ErrorIs(t, err, service.ErrAccountQualityHeld)
		require.False(t, admitted)
	})

	for _, state := range []struct {
		status      string
		schedulable bool
		want        string
	}{
		{"active", true, "active"},
		{"quality_paused", true, "active"},
		{"quality_paused", false, "active"},
		{"disabled", false, "disabled"},
	} {
		t.Run("stale projection "+state.status+" scheduling "+map[bool]string{true: "on", false: "off"}[state.schedulable], func(t *testing.T) {
			f := newPassRateFixture(t, 2)
			f.exec(`UPDATE accounts SET status=$1,schedulable=$2,extra='{"setting":"keep","active_test_model_holds":{"model-a":"old hold"},"active_pass_rate_holds":{"model-a":"old hold"},"active_test_all_models_paused":true,"quality_protection_reason":"old hold","quality_protection_trial":{}}' WHERE id=62`, state.status, state.schedulable)
			require.NoError(t, recoveryAccountRepo(f).ClearAccountQualityProtection(f.ctx, 62))
			assertRecoveredQuality(t, f, state.want, state.schedulable)
		})
	}

	t.Run("failed outbox rolls back holds and revocation", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.result("fail")
		row := f.result("fail")
		f.exec(`DELETE FROM scheduler_outbox; ALTER TABLE scheduler_outbox ADD CONSTRAINT reject_recovery CHECK (event_type<>'account_changed')`)
		require.Error(t, recoveryAccountRepo(f).ClearAccountQualityProtection(f.ctx, 62))
		require.True(t, f.summary("model-a").Blocked)
		var revoked bool
		require.NoError(t, f.db.QueryRowContext(f.ctx, `SELECT protection_revoked FROM scheduled_test_results WHERE id=$1`, row.ID).Scan(&revoked))
		require.False(t, revoked)
	})

	t.Run("recovery serializes behind an in flight quality writer", func(t *testing.T) {
		f := newPassRateFixture(t, 2)
		f.begin()
		f.exec(`UPDATE accounts SET extra='{"setting":"keep"}' WHERE id=62`)
		tx, err := f.db.BeginTx(f.ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		_, err = lockProtectionPlan(f.ctx, tx, f.plan.ID)
		require.NoError(t, err)
		recovered := make(chan error, 1)
		go func() { recovered <- recoveryAccountRepo(f).ClearAccountQualityProtection(f.ctx, 62) }()
		require.Eventually(t, func() bool {
			var waiting bool
			err := f.db.QueryRowContext(f.ctx, `SELECT EXISTS(SELECT 1 FROM pg_stat_activity WHERE pid<>pg_backend_pid() AND wait_event_type='Lock' AND query LIKE '%SELECT p.id FROM scheduled_test_plans p%')`).Scan(&waiting)
			return err == nil && waiting
		}, 5*time.Second, 10*time.Millisecond)
		_, err = tx.ExecContext(f.ctx, `UPDATE scheduled_test_protection_states SET blocked=TRUE,reason='in flight failure' WHERE account_id=62`)
		require.NoError(t, err)
		require.NoError(t, reconcileProtectionAccount(f.ctx, tx, 62))
		require.NoError(t, tx.Commit())
		select {
		case err := <-recovered:
			require.NoError(t, err)
		case <-f.ctx.Done():
			t.Fatal("recovery did not finish after quality writer committed")
		}
		assertRecoveredQuality(t, f, "active", true)
	})
}
