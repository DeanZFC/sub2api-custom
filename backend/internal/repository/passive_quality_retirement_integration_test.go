//go:build integration

package repository

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestCustomPassiveRuleRetirementIntegration(t *testing.T) {
	ctx, db, repo := passiveDB(t)
	// Model the previous release with overlapping, non-overlapping and disabled
	// custom rules, plus both generations of supported group targets.
	legacy := newPassivePolicy()
	require.NoError(t, repo.SavePolicy(ctx, &legacy))
	other := newPassivePolicy()
	other.GroupIDs, other.Enabled, other.SchemaVersion = []int64{9}, false, 2
	require.NoError(t, repo.SavePolicy(ctx, &other))
	target := newGroupTargetPolicy(t, ctx, db, repo)
	cache := service.PassiveQualityPolicy{PassiveQualityConfig: service.PassiveQualityConfig{Purpose: "cache_target", GroupIDs: []int64{9}, CacheRate: service.PassiveQualityMetric{Enabled: true, Pause: 73, Recover: 80}}}
	require.NoError(t, repo.SavePolicy(ctx, &cache))
	var beforeTarget, beforeCache string
	require.NoError(t, db.QueryRow(`SELECT row_to_json(p)::text FROM passive_quality_policies p WHERE id=$1`, target.ID).Scan(&beforeTarget))
	require.NoError(t, db.QueryRow(`SELECT row_to_json(p)::text FROM passive_quality_policies p WHERE id=$1`, cache.ID).Scan(&beforeCache))
	for _, p := range []service.PassiveQualityPolicy{legacy, other, target, cache} {
		_, err := db.Exec(`INSERT INTO passive_quality_states(policy_id,account_id,model_key,data) VALUES($1,62,'model',jsonb_build_object('policy_id',$1::bigint,'account_id',62,'model','model','phase','paused','evidence',jsonb_build_object('samples',17)));
`, p.ID)
		require.NoError(t, err)
		_, err = db.Exec(`INSERT INTO passive_quality_leases(id,policy_id,account_id,model_key,generation) VALUES('inflight',$1,62,'model',1)`, p.ID)
		require.NoError(t, err)
	}
	_, err := db.Exec(`CREATE TABLE quality_management_config_archive(kind TEXT,source_id BIGINT,config JSONB,archived_at TIMESTAMPTZ DEFAULT now(),PRIMARY KEY(kind,source_id));
INSERT INTO scheduled_test_pass_rate_states(account_id,model_key,blocked) VALUES(62,'model',true);
INSERT INTO accounts(id,name,extra) VALUES(64,'null','{"passive_quality":null}'),(65,'malformed','{"passive_quality":{"policy_id":"bad"}}'),(66,'target only','{}');
UPDATE accounts SET status='disabled',schedulable=false WHERE id=62;`)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET extra=jsonb_build_object('keep','yes','quality_paused',true,'passive_quality',jsonb_build_array(jsonb_build_object('policy_id',$1::bigint,'phase','paused'),jsonb_build_object('policy_id',$2::bigint,'phase','paused'),jsonb_build_object('policy_id',$3::bigint,'phase','trial'),jsonb_build_object('policy_id',$4::bigint,'phase','paused'))) WHERE id=62`, legacy.ID, target.ID, other.ID, cache.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET extra=jsonb_build_object('keep','yes','passive_quality',jsonb_build_array(jsonb_build_object('policy_id',$1::bigint,'phase','paused'))) WHERE id=63`, other.ID)
	require.NoError(t, err)
	_, err = db.Exec(`UPDATE accounts SET extra=jsonb_build_object('passive_quality',jsonb_build_array(jsonb_build_object('policy_id',$1::bigint,'phase','paused'))) WHERE id=66`, target.ID)
	require.NoError(t, err)
	var expectedHolds string
	require.NoError(t, db.QueryRow(`SELECT jsonb_build_object('keep','yes','quality_paused',true,'passive_quality',jsonb_build_array(jsonb_build_object('policy_id',$1::bigint,'phase','paused'),jsonb_build_object('policy_id',$2::bigint,'phase','paused')))::text`, target.ID, cache.ID).Scan(&expectedHolds))
	migration, err := os.ReadFile("../../migrations/318_retire_custom_passive_rules.sql")
	require.NoError(t, err)
	_, err = db.Exec(string(migration))
	require.NoError(t, err)
	policies, err := repo.ListPolicies(ctx)
	require.NoError(t, err)
	require.Len(t, policies, 2)
	for _, p := range policies {
		require.True(t, p.IsGroupQualityTarget())
	}
	for id, before := range map[int64]string{target.ID: beforeTarget, cache.ID: beforeCache} {
		var after string
		require.NoError(t, db.QueryRow(`SELECT row_to_json(p)::text FROM passive_quality_policies p WHERE id=$1`, id).Scan(&after))
		require.JSONEq(t, before, after, "target config, version and evidence boundary survive")
	}
	for _, p := range []service.PassiveQualityPolicy{legacy, other} {
		var archive string
		require.NoError(t, db.QueryRow(`SELECT config::text FROM quality_management_config_archive WHERE kind='passive_retired' AND source_id=$1`, p.ID).Scan(&archive))
		expected, err := json.Marshal(p.PassiveQualityConfig)
		require.NoError(t, err)
		require.JSONEq(t, string(expected), archive)
	}
	for _, table := range []string{"passive_quality_states", "passive_quality_leases"} {
		var count int
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table+` WHERE policy_id IN ($1,$2)`, legacy.ID, other.ID).Scan(&count))
		require.Zero(t, count)
		require.NoError(t, db.QueryRow(`SELECT count(*) FROM `+table+` WHERE policy_id IN ($1,$2)`, target.ID, cache.ID).Scan(&count))
		require.Equal(t, 2, count)
	}
	var extra, status string
	var schedulable, blocked bool
	require.NoError(t, db.QueryRow(`SELECT extra::text,status,schedulable FROM accounts WHERE id=62`).Scan(&extra, &status, &schedulable))
	require.JSONEq(t, expectedHolds, extra)
	require.Equal(t, "disabled", status)
	require.False(t, schedulable)
	require.NoError(t, db.QueryRow(`SELECT blocked FROM scheduled_test_pass_rate_states WHERE account_id=62`).Scan(&blocked))
	require.True(t, blocked)
	require.NoError(t, db.QueryRow(`SELECT extra::text FROM accounts WHERE id=63`).Scan(&extra))
	require.JSONEq(t, `{"keep":"yes"}`, extra)
	for id, expected := range map[int64]string{64: `{"passive_quality":null}`, 65: `{"passive_quality":{"policy_id":"bad"}}`} {
		require.NoError(t, db.QueryRow(`SELECT extra::text FROM accounts WHERE id=$1`, id).Scan(&extra))
		require.JSONEq(t, expected, extra)
	}
	var count int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM passive_quality_events WHERE kind='custom_rule_retired' AND detail->>'reason'='custom_rule_retired'`).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE event_type='account_changed' AND account_id IN (62,63)`).Scan(&count))
	require.Equal(t, 2, count)
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM scheduler_outbox WHERE account_id IN (64,65,66)`).Scan(&count))
	require.Zero(t, count)
	for _, purpose := range []string{`{}`, `{"purpose":null}`, `{"purpose":""}`, `{"purpose":"custom"}`} {
		_, err := db.Exec(`INSERT INTO passive_quality_policies(config) VALUES($1)`, purpose)
		require.ErrorContains(t, err, "passive_quality_group_targets_only")
	}
	_, err = db.Exec(`UPDATE passive_quality_policies SET config=config-'purpose' WHERE id=$1`, target.ID)
	require.ErrorContains(t, err, "passive_quality_group_targets_only")
	target.Name = "Updated group target"
	require.NoError(t, repo.SavePolicy(ctx, &target), "group targets remain editable after retirement")
	retired := newPassivePolicy()
	require.ErrorContains(t, repo.SavePolicy(ctx, &retired), "passive_quality_group_targets_only", "old repository writes cannot recreate retired rules")
}
