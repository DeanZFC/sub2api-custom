//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func passivePoolPolicy(t *testing.T, ctx context.Context, r *passiveQualityRepository) service.PassiveQualityPolicy {
	t.Helper()
	p := newPassivePolicy()
	p.SchemaVersion = 2
	p.CacheRate = service.PassiveQualityMetric{Enabled: true, Pause: 20, Recover: 50}
	d := service.DefaultPassiveQualityCacheConfig()
	p.CacheProtection = &d
	require.NoError(t, r.SavePolicy(ctx, &p))
	return p
}

func TestPassiveQualityPoolHardEligibilityIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	_, err := db.Exec(`ALTER TABLE accounts ADD COLUMN platform TEXT DEFAULT 'openai',ADD COLUMN type TEXT DEFAULT 'apikey',ADD COLUMN credentials JSONB DEFAULT '{}',ADD COLUMN overload_until TIMESTAMPTZ,ADD COLUMN expires_at TIMESTAMPTZ,ADD COLUMN auto_pause_on_expired BOOLEAN DEFAULT true,ADD COLUMN parent_account_id BIGINT;`)
	require.NoError(t, err)
	p := passivePoolPolicy(t, ctx, r)
	allows := func() bool {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		allow, err := passiveCachePoolAllows(ctx, tx, p, 62, "model", time.Now())
		require.NoError(t, err)
		return allow
	}
	require.True(t, allows(), "two healthy model-compatible accounts leave one full allocation reserve")
	future := time.Now().Add(time.Hour).Format(time.RFC3339)
	cases := []struct {
		name               string
		extra, credentials map[string]any
		column             string
	}{
		{name: "model_cooldown", extra: map[string]any{"model_rate_limits": map[string]any{"model": map[string]any{"rate_limit_reset_at": future}}}},
		{name: "mapped_model_cooldown", extra: map[string]any{"model_rate_limits": map[string]any{"upstream-model": map[string]any{"rate_limit_reset_at": future}}}, credentials: map[string]any{"model_mapping": map[string]any{"model": "upstream-model"}}},
		{name: "quota_exhausted", extra: map[string]any{"quota_limit": 1.0, "quota_used": 2.0}},
		{name: "model_unsupported", credentials: map[string]any{"model_mapping": map[string]any{"other": "other"}}},
		{name: "scheduled_trial", extra: map[string]any{"quality_protection_trial": true}},
		{name: "billing_ceiling", credentials: map[string]any{"api_key": "test-key", "base_url": "https://upstream.example"}, extra: map[string]any{"upstream_billing_rate_limit": 1.0, "upstream_billing_probe_enabled": true, "upstream_billing_probe": map[string]any{"status": "ok", "data": map[string]any{"billing_scope": "token", "resolved_rate_multiplier": 2.0, "peak_rate_enabled": false}}}},
		{name: "disabled", column: "status='disabled'"},
		{name: "manual_unschedulable", column: "schedulable=false"},
		{name: "overload", column: "overload_until=now()+INTERVAL '1 hour'"},
		{name: "expired", column: "expires_at=now()-INTERVAL '1 hour'"},
		{name: "shadow", column: "parent_account_id=62"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := db.Exec(`UPDATE accounts SET extra='{}',credentials='{}',status='active',schedulable=true,overload_until=NULL,expires_at=NULL,parent_account_id=NULL WHERE id=63`)
			require.NoError(t, err)
			if tc.extra != nil {
				raw, err := json.Marshal(tc.extra)
				require.NoError(t, err)
				_, err = db.Exec(`UPDATE accounts SET extra=$1 WHERE id=63`, raw)
				require.NoError(t, err)
			}
			if tc.credentials != nil {
				raw, err := json.Marshal(tc.credentials)
				require.NoError(t, err)
				_, err = db.Exec(`UPDATE accounts SET credentials=$1 WHERE id=63`, raw)
				require.NoError(t, err)
			}
			if tc.column != "" {
				_, err = db.Exec(`UPDATE accounts SET ` + tc.column + ` WHERE id=63`)
				require.NoError(t, err)
			}
			require.False(t, allows(), "unusable account cannot be counted as serving reserve")
		})
	}
}

func TestPassiveQualityPoolOverlappingPoliciesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	_, err := db.Exec(`INSERT INTO accounts(id,name) VALUES(64,'C'),(65,'D');INSERT INTO account_groups VALUES(64,8),(65,8),(62,9),(63,9),(64,9),(65,9);`)
	require.NoError(t, err)
	first := passivePoolPolicy(t, ctx, r)
	second := passivePoolPolicy(t, ctx, r)
	second.GroupIDs = []int64{9}
	second.CacheProtection.MaxReducedPercent = 25
	require.NoError(t, r.SavePolicy(ctx, &second))
	// The first policy also covers group 9, whose strictest overlapping cap is 25%.
	first.GroupIDs = []int64{8, 9}
	require.NoError(t, r.SavePolicy(ctx, &first))
	state := service.PassiveQualityState{PolicyID: second.ID, PolicyName: second.Name, AccountID: 63, Phase: "healthy", Mode: "enforce", Cache: map[string]service.PassiveQualityCacheState{"model": {Phase: "reduced", AllocationPercent: 25}}}
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	require.NoError(t, savePassiveState(ctx, tx, state))
	require.NoError(t, tx.Commit())
	tx, err = db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	allow, err := passiveCachePoolAllows(ctx, tx, first, 62, "model", time.Now())
	require.NoError(t, err)
	require.False(t, allow, "second policy reduction consumes first policy overlapping group capacity")
	allow, err = passiveCachePoolAllows(ctx, tx, first, 63, "model", time.Now())
	require.NoError(t, err)
	require.True(t, allow, "same account is counted only once across policies")
}

func TestPassiveQualityPoolConcurrentExpansionIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	_, err := db.Exec(`INSERT INTO accounts(id,name) VALUES(64,'C'),(65,'D');INSERT INTO account_groups VALUES(64,8),(65,8);`)
	require.NoError(t, err)
	p := passivePoolPolicy(t, ctx, r)
	var accepted atomic.Int32
	var wg sync.WaitGroup
	for _, aid := range []int64{62, 63, 64, 65} {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			err := func() error {
				tx, err := db.BeginTx(ctx, &sql.TxOptions{})
				if err != nil {
					return err
				}
				defer tx.Rollback()
				if _, err = tx.ExecContext(ctx, `SELECT pg_advisory_xact_lock(1937006961,3)`); err != nil {
					return err
				}
				allow, err := passiveCachePoolAllows(ctx, tx, p, id, "model", time.Now())
				if err != nil {
					return err
				}
				if allow {
					s := service.PassiveQualityState{PolicyID: p.ID, PolicyName: p.Name, AccountID: id, Phase: "healthy", Mode: "enforce", Cache: map[string]service.PassiveQualityCacheState{"model": {Phase: "reduced", AllocationPercent: 25}}}
					if err = savePassiveState(ctx, tx, s); err != nil {
						return err
					}
					accepted.Add(1)
				}
				return tx.Commit()
			}()
			if err != nil {
				t.Errorf("concurrent expansion: %v", err)
			}
		}(aid)
	}
	wg.Wait()
	require.Equal(t, int32(2), accepted.Load(), "shared pool lock caps simultaneous distinct reductions at50%")
}

func TestPassiveQualityPoolProtectsAccountGroupsOutsidePolicyIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	// Policy covers only G1. Its account/model projection also affects A in G2.
	_, err := db.Exec(`INSERT INTO account_groups VALUES(62,9)`)
	require.NoError(t, err)
	p := passivePoolPolicy(t, ctx, r)
	check := func(want bool) {
		t.Helper()
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		defer tx.Rollback()
		allow, err := passiveCachePoolAllows(ctx, tx, p, 62, "model", time.Now())
		require.NoError(t, err)
		require.Equal(t, want, allow)
	}
	check(false) // G1 could reduce A, but G2 would lose its only full allocation.
	_, err = db.Exec(`INSERT INTO account_groups VALUES(63,9)`)
	require.NoError(t, err)
	check(true)
	q := passivePoolPolicy(t, ctx, r)
	q.GroupIDs = []int64{9}
	q.CacheProtection.MaxReducedPercent = 25
	require.NoError(t, r.SavePolicy(ctx, &q))
	check(false) // G2 strict policy must also apply even though p.GroupIDs is G1 only.
}
