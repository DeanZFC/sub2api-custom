//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func groupTargetState(t *testing.T, ctx context.Context, db *sql.DB, p service.PassiveQualityPolicy, aid int64, model, phase string) {
	t.Helper()
	now := time.Now()
	s := service.PassiveQualityState{PolicyID: p.ID, AccountID: aid, Model: model, Scope: p.Scope, Version: p.DecisionVersion(), Purpose: p.Purpose, Phase: phase, UpdatedAt: now, TrialStartedAt: now.Add(-time.Minute), TrialEndsAt: now.Add(time.Hour)}
	saveActiveStateForTest(t, ctx, db, s)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	require.NoError(t, reconcilePassiveAccount(ctx, tx, aid))
	require.NoError(t, tx.Commit())
}

func TestActiveRecoveryLegacyGroupHoldsNeverAdmitCustomersIntegration(t *testing.T) {
	for _, purpose := range []string{"group_quality_target", "cache_target"} {
		for _, mode := range []string{"", "traffic", "active"} {
			for _, phase := range []string{"paused", "probing", "trial"} {
				t.Run(purpose+"/"+mode+"/"+phase, func(t *testing.T) {
					ctx, db, r := passiveDB(t)
					p := newGroupTargetPolicy(t, ctx, db, r)
					_, err := db.Exec("UPDATE passive_quality_policies SET config=config||jsonb_build_object('purpose',$2::text,'recovery_mode',$3::text,'traffic_mode','availability') WHERE id=$1", p.ID, purpose, mode)
					require.NoError(t, err)
					groupTargetState(t, ctx, db, p, 62, "A", phase)
					allowed, reserved, err := r.AcquirePassiveQuality(ctx, 62, "A", "customer")
					require.NoError(t, err)
					require.False(t, allowed)
					require.False(t, reserved)
					allowed, err = groupQualityFinalAdmission(ctx, db, 62, "A")
					require.ErrorIs(t, err, service.ErrAccountQualityHeld)
					require.False(t, allowed)
					allowed, reserved, err = r.AcquirePassiveQuality(ctx, 63, "A", "healthy-peer")
					require.NoError(t, err)
					require.True(t, allowed)
					require.False(t, reserved)
					allowed, err = groupQualityFinalAdmission(ctx, db, 62, "B")
					require.NoError(t, err)
					require.True(t, allowed, "another public model remains available")
					var count int
					require.NoError(t, db.QueryRow("SELECT count(*) FROM passive_quality_leases").Scan(&count))
					require.Zero(t, count)
					require.NoError(t, db.QueryRow("SELECT count(*) FROM group_quality_traffic_leases").Scan(&count))
					require.Zero(t, count)
				})
			}
		}
	}
}

func TestActiveRecoveryFinalAdmissionWaitsForConcurrentHoldIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	allowed, err := groupQualityFinalAdmission(ctx, db, 62, "A")
	require.NoError(t, err)
	require.True(t, allowed)
	tx, err := db.BeginTx(ctx, nil)
	require.NoError(t, err)
	defer tx.Rollback()
	_, err = lockPassiveAccount(ctx, tx, 62)
	require.NoError(t, err)
	state := service.PassiveQualityState{PolicyID: p.ID, AccountID: 62, Model: "A", Scope: "model", Version: p.DecisionVersion(), Purpose: p.Purpose, Phase: "trial", UpdatedAt: time.Now()}
	require.NoError(t, savePassiveState(ctx, tx, state))
	result := make(chan bool, 1)
	failure := make(chan error, 1)
	go func() { ok, e := groupQualityFinalAdmission(ctx, db, 62, "A"); result <- ok; failure <- e }()
	select {
	case <-result:
		t.Fatal("admission bypassed an in-flight protection writer")
	case <-time.After(75 * time.Millisecond):
	}
	require.NoError(t, tx.Commit())
	require.False(t, <-result)
	require.ErrorIs(t, <-failure, service.ErrAccountQualityHeld)
}

func TestActiveRecoveryCrossPolicyExclusionsAndManualHoldsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	groupTargetState(t, ctx, db, p, 62, "A", "trial")
	other := newPassivePolicy()
	other.Model = "A"
	require.NoError(t, r.SavePolicy(ctx, &other))
	groupTargetState(t, ctx, db, other, 62, "A", "paused")
	_, err := db.Exec("UPDATE passive_quality_policies SET config=jsonb_set(config,'{excluded_account_ids}','[62]') WHERE id=$1", p.ID)
	require.NoError(t, err)
	allowed, reserved, err := r.AcquirePassiveQuality(ctx, 62, "A", "other-policy")
	require.NoError(t, err)
	require.False(t, allowed)
	require.False(t, reserved)
	_, err = db.Exec("DELETE FROM passive_quality_states WHERE policy_id=$1", other.ID)
	require.NoError(t, err)
	allowed, _, err = r.AcquirePassiveQuality(ctx, 62, "A", "excluded")
	require.NoError(t, err)
	require.True(t, allowed)
	_, err = db.Exec("UPDATE passive_quality_policies SET config=jsonb_set(config,'{excluded_account_ids}','[]') WHERE id=$1", p.ID)
	require.NoError(t, err)
	_, err = db.Exec("DELETE FROM passive_quality_states WHERE policy_id=$1", p.ID)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE accounts SET status='disabled',schedulable=false WHERE id=62")
	require.NoError(t, err)
	allowed, err = groupQualityFinalAdmission(ctx, db, 62, "A")
	require.NoError(t, err)
	require.False(t, allowed)
}

func TestActiveRecoveryRetirementMigrationArchivesAndFencesLeasesIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	p.TTFT = service.PassiveQualityMetric{Enabled: false, Pause: 4321, Recover: 3456}
	p.TPS = service.PassiveQualityMetric{Enabled: true, Pause: 27, Recover: 34}
	require.NoError(t, r.SavePolicy(ctx, &p))
	_, err := db.Exec("UPDATE passive_quality_policies SET config=config||'{\"recovery_mode\":\"traffic\",\"traffic_mode\":\"availability\",\"excluded_account_ids\":[63],\"trial_requests\":99}'::jsonb WHERE id=$1", p.ID)
	require.NoError(t, err)
	groupTargetState(t, ctx, db, p, 62, "A", "trial")
	groupTargetState(t, ctx, db, p, 63, "A", "trial")
	other := newPassivePolicy()
	other.Model = "B"
	require.NoError(t, r.SavePolicy(ctx, &other))
	groupTargetState(t, ctx, db, other, 62, "B", "paused")
	_, err = db.Exec("CREATE TABLE quality_management_config_archive(kind TEXT,source_id BIGINT,config JSONB,PRIMARY KEY(kind,source_id))")
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO passive_quality_leases(id,policy_id,account_id,model_key,generation) VALUES('old',$1,62,'A',0),('other',$2,62,'',0)", p.ID, other.ID)
	require.NoError(t, err)
	_, err = db.Exec("INSERT INTO group_quality_traffic_leases(lease_id,policy_id,policy_version,account_id,group_id,model,request_key,validation,fallback,fallback_expires_at,reserved_input) VALUES('old',$1,$2,62,8,'A','old',true,true,now()+interval '1 hour',123)", p.ID, p.Version)
	require.NoError(t, err)
	_, err = db.Exec("UPDATE accounts SET status='disabled',schedulable=false,extra=extra||'{\"manual_note\":\"keep\"}'::jsonb WHERE id=62")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/323_retire_group_quality_traffic_recovery.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	var cfg, archive, extra []byte
	require.NoError(t, db.QueryRow("SELECT config FROM passive_quality_policies WHERE id=$1", p.ID).Scan(&cfg))
	var updated service.PassiveQualityConfig
	require.NoError(t, json.Unmarshal(cfg, &updated))
	require.Equal(t, "active", updated.RecoveryMode)
	require.Equal(t, "strict", updated.TrafficMode)
	require.Equal(t, 3600, updated.WindowSeconds)
	require.Zero(t, updated.MaxSamples)
	require.Equal(t, p.TTFT, updated.TTFT)
	require.Equal(t, p.TPS, updated.TPS)
	require.Equal(t, []int64{63}, updated.ExcludedAccountIDs)
	require.Equal(t, 2, updated.Confirmations)
	require.Equal(t, 5, updated.RecoverySamples)
	require.Nil(t, updated.Stability, "an omitted stability object must retain active recovery defaults")
	require.Equal(t, 300, updated.GroupTargetStability().PerformancePauseSeconds)
	require.NoError(t, db.QueryRow("SELECT config FROM quality_management_config_archive WHERE kind='retired_group_traffic_recovery' AND source_id=$1", p.ID).Scan(&archive))
	require.Contains(t, string(archive), "traffic")
	require.Contains(t, string(archive), "passive_leases")
	require.Contains(t, string(archive), "traffic_leases")
	var phase, status string
	var sched bool
	var count int
	require.NoError(t, db.QueryRow("SELECT data->>'phase' FROM passive_quality_states WHERE policy_id=$1 AND account_id=62", p.ID).Scan(&phase))
	require.Equal(t, "paused", phase)
	require.NoError(t, db.QueryRow("SELECT count(*) FROM passive_quality_leases WHERE policy_id=$1", p.ID).Scan(&count))
	require.Zero(t, count)
	require.NoError(t, db.QueryRow("SELECT count(*) FROM passive_quality_leases WHERE policy_id=$1", other.ID).Scan(&count))
	require.Equal(t, 1, count)
	require.NoError(t, db.QueryRow("SELECT status,schedulable,extra FROM accounts WHERE id=62").Scan(&status, &sched, &extra))
	require.Equal(t, "disabled", status)
	require.False(t, sched)
	require.Contains(t, string(extra), "keep")
	require.Contains(t, string(extra), fmt.Sprintf("\"policy_id\": %d", other.ID))
	require.NoError(t, db.QueryRow("SELECT count(*) FROM group_quality_traffic_leases WHERE released_at IS NOT NULL AND fallback_expires_at<=now()").Scan(&count))
	require.Equal(t, 1, count)
	_, err = db.Exec("UPDATE group_quality_traffic_leases SET fallback_expires_at=now()+interval '1 hour' WHERE lease_id='old'")
	require.Error(t, err)
	_, err = db.Exec("INSERT INTO passive_quality_leases(id,policy_id,account_id,model_key,generation) VALUES('stale-instance',$1,62,'A',1)", p.ID)
	require.Error(t, err)
	_, err = db.Exec("INSERT INTO group_quality_traffic_leases(lease_id,policy_id,policy_version,account_id,group_id,model,request_key,validation,reserved_input) VALUES('stale-instance',$1,$2,62,8,'A','stale-instance',true,1)", p.ID, p.Version)
	require.Error(t, err)
	var archivedLeaseCount, archivedReservedInput int
	require.NoError(t, db.QueryRow("SELECT jsonb_array_length(config->'traffic_leases'),(config->'traffic_leases'->0->>'reserved_input')::int FROM quality_management_config_archive WHERE kind='retired_group_traffic_recovery' AND source_id=$1", p.ID).Scan(&archivedLeaseCount, &archivedReservedInput))
	require.Equal(t, 1, archivedLeaseCount)
	require.Equal(t, 123, archivedReservedInput)
	require.NoError(t, db.QueryRow("SELECT count(*) FROM group_quality_traffic_leases WHERE policy_id=$1 AND fallback AND reserved_input=123", p.ID).Scan(&count))
	require.Equal(t, 1, count, "retired lease records remain available for historical request classification")
}

func TestActiveRecoveryRetirementPreservesExplicitStabilityAndDisabledMetricsIntegration(t *testing.T) {
	ctx, db, r := passiveDB(t)
	p := newGroupTargetPolicy(t, ctx, db, r)
	_, err := db.Exec("UPDATE passive_quality_policies SET config=config||'{\"purpose\":\"cache_target\",\"recovery_mode\":\"traffic\",\"stability\":{\"availability_pause_seconds\":0,\"performance_pause_seconds\":0,\"recovery_seconds\":0,\"new_session_percent\":20},\"ttft_p90_ms\":{\"enabled\":false,\"pause\":4321,\"recover\":3456}}'::jsonb WHERE id=$1", p.ID)
	require.NoError(t, err)
	other := newPassivePolicy()
	require.NoError(t, r.SavePolicy(ctx, &other))
	groupTargetState(t, ctx, db, other, 62, "model", "paused")
	var beforeConfig, beforeState []byte
	require.NoError(t, db.QueryRow("SELECT config FROM passive_quality_policies WHERE id=$1", other.ID).Scan(&beforeConfig))
	require.NoError(t, db.QueryRow("SELECT data FROM passive_quality_states WHERE policy_id=$1", other.ID).Scan(&beforeState))
	_, err = db.Exec("CREATE TABLE quality_management_config_archive(kind TEXT,source_id BIGINT,config JSONB,PRIMARY KEY(kind,source_id))")
	require.NoError(t, err)
	migration, err := os.ReadFile("../../migrations/323_retire_group_quality_traffic_recovery.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	var raw, afterConfig, afterState []byte
	require.NoError(t, db.QueryRow("SELECT config FROM passive_quality_policies WHERE id=$1", p.ID).Scan(&raw))
	var config map[string]any
	require.NoError(t, json.Unmarshal(raw, &config))
	stability := config["stability"].(map[string]any)
	require.Equal(t, float64(0), stability["availability_pause_seconds"])
	require.Equal(t, float64(0), stability["performance_pause_seconds"])
	require.Equal(t, float64(0), stability["recovery_seconds"])
	require.NotContains(t, stability, "new_session_percent")
	require.Equal(t, false, config["ttft_p90_ms"].(map[string]any)["enabled"])
	require.Equal(t, float64(4321), config["ttft_p90_ms"].(map[string]any)["pause"])
	require.NoError(t, db.QueryRow("SELECT config FROM passive_quality_policies WHERE id=$1", other.ID).Scan(&afterConfig))
	require.NoError(t, db.QueryRow("SELECT data FROM passive_quality_states WHERE policy_id=$1", other.ID).Scan(&afterState))
	require.JSONEq(t, string(beforeConfig), string(afterConfig))
	require.JSONEq(t, string(beforeState), string(afterState))
}

func TestActiveRecoveryRetirementNormalizesImpossibleRecoverySpansIntegration(t *testing.T) {
	for _, seconds := range []int{120, 539, 540, 600, 3600} {
		t.Run(fmt.Sprint(seconds), func(t *testing.T) {
			ctx, db, r := passiveDB(t)
			p := newGroupTargetPolicy(t, ctx, db, r)
			_, err := db.Exec("UPDATE passive_quality_policies SET config=config||jsonb_build_object('confirmations',10,'recovery_samples',2000,'stability',jsonb_build_object('availability_pause_seconds',60,'performance_pause_seconds',300,'recovery_seconds',$2::int)) WHERE id=$1", p.ID, seconds)
			require.NoError(t, err)
			_, err = db.Exec("CREATE TABLE quality_management_config_archive(kind TEXT,source_id BIGINT,config JSONB,PRIMARY KEY(kind,source_id))")
			require.NoError(t, err)
			migration, err := os.ReadFile("../../migrations/323_retire_group_quality_traffic_recovery.sql")
			require.NoError(t, err)
			_, err = db.Exec(string(migration))
			require.NoError(t, err)
			var raw []byte
			require.NoError(t, db.QueryRow("SELECT config FROM passive_quality_policies WHERE id=$1", p.ID).Scan(&raw))
			var updated service.PassiveQualityConfig
			require.NoError(t, json.Unmarshal(raw, &updated))
			want := seconds
			if want >= 540 {
				want = 120
			}
			require.Equal(t, want, updated.GroupTargetStability().RecoverySeconds)
			require.Equal(t, 2, updated.Confirmations)
			require.Equal(t, 5, updated.RecoverySamples)
			require.NoError(t, updated.Validate(), "migrated recovery must fit the active request and time budget")
			var original int
			require.NoError(t, db.QueryRow("SELECT (config->'policy'->'config'->'stability'->>'recovery_seconds')::int FROM quality_management_config_archive WHERE kind='retired_group_traffic_recovery' AND source_id=$1", p.ID).Scan(&original))
			require.Equal(t, seconds, original)
		})
	}
}
