//go:build integration

package repository

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func TestGroupQualityConsolidationPreservesOtherGroupsIntegration(t *testing.T) {
	ctx, db, repo := passiveDB(t)
	_, err := db.Exec(`UPDATE account_groups SET group_id=9 WHERE account_id=63; INSERT INTO accounts(id,name) VALUES(64,'Shared'); INSERT INTO account_groups VALUES(64,8),(64,9)`)
	require.NoError(t, err)
	legacy := newPassivePolicy()
	legacy.GroupIDs = []int64{8, 9}
	require.NoError(t, repo.SavePolicy(ctx, &legacy))
	for _, aid := range []int64{62, 63, 64} {
		tx, err := db.BeginTx(ctx, nil)
		require.NoError(t, err)
		state := service.PassiveQualityState{PolicyID: legacy.ID, PolicyName: legacy.Name, AccountID: aid, Model: "model", Scope: "model", Version: legacy.DecisionVersion(), Phase: "paused"}
		require.NoError(t, savePassiveState(ctx, tx, state))
		require.NoError(t, reconcilePassiveAccount(ctx, tx, aid))
		require.NoError(t, tx.Commit())
	}
	target := newGroupTargetPolicy(t, ctx, db, repo)
	updated, err := scanPassivePolicy(db.QueryRow(`SELECT id,version,updated_at,config FROM passive_quality_policies WHERE id=$1`, legacy.ID))
	require.NoError(t, err)
	require.Equal(t, []int64{9}, updated.GroupIDs)
	require.Equal(t, []int64{8}, updated.ConsolidatedGroupIDs)
	require.True(t, updated.Enabled)
	require.Equal(t, legacy.ErrorRate, updated.ErrorRate)
	require.Equal(t, legacy.DecisionVersion(), updated.DecisionVersion())
	require.WithinDuration(t, legacy.EvidenceSince(), updated.EvidenceSince(), time.Microsecond)
	for _, aid := range []int64{62, 63, 64} {
		states, err := repo.ListStates(ctx, aid)
		require.NoError(t, err)
		var extra []byte
		require.NoError(t, db.QueryRow(`SELECT extra FROM accounts WHERE id=$1`, aid).Scan(&extra))
		if aid == 62 {
			require.Empty(t, states)
			require.NotContains(t, string(extra), "passive_quality")
		} else {
			require.Len(t, states, 1)
			require.Contains(t, string(extra), "passive_quality")
		}
	}
	// No-op target saves preserve both decision identities; callers cannot recreate overlap.
	require.NoError(t, repo.SavePolicy(ctx, &target))
	updated.GroupIDs = []int64{8, 9}
	require.ErrorContains(t, repo.SavePolicy(ctx, &updated), "managed by group quality targets")
	var events int
	require.NoError(t, db.QueryRow(`SELECT count(*) FROM passive_quality_events WHERE kind='group_target_consolidated'`).Scan(&events))
	require.Equal(t, 1, events)
}

func TestGroupQualityUnifiedMigrationIntegration(t *testing.T) {
	for _, customScore := range []bool{false, true} {
		t.Run(map[bool]string{false: "old-defaults", true: "custom-score"}[customScore], func(t *testing.T) {
			ctx, db, repo := passiveDB(t)
			legacy := newPassivePolicy()
			require.NoError(t, repo.SavePolicy(ctx, &legacy))
			_, err := db.Exec(`INSERT INTO passive_quality_states(policy_id,account_id,model_key,data) VALUES($1,62,'model',jsonb_build_object('phase','paused','policy_id',$1::bigint));
`, legacy.ID)
			require.NoError(t, err)
			_, err = db.Exec(`UPDATE accounts SET extra=jsonb_build_object('passive_quality',jsonb_build_array(jsonb_build_object('policy_id',$1::bigint,'phase','paused')),'keep','yes') WHERE id=62`, legacy.ID)
			require.NoError(t, err)
			// Insert directly to represent pre-upgrade duplicate owners.
			_, err = db.Exec(`INSERT INTO passive_quality_policies(config) VALUES('{"purpose":"group_quality_target","group_ids":[8],"enabled":true,"cache_rate":{"enabled":true,"pause":74,"recover":79}}');
CREATE TABLE scheduled_test_plans(id BIGINT PRIMARY KEY,protection JSONB);
INSERT INTO scheduled_test_plans VALUES(1,'{"mode":"all_pass","rules":[{"test_definition_id":3,"expected_answer":"21","pass_rate":{"enabled":true,"window_size":10,"recover_consecutive":3,"pause_below":80,"recover_at_least":90}}]}'),(2,'{"mode":"combined","rules":[{"pass_rate":{"enabled":true,"window_size":10}}]}');
INSERT INTO scheduled_test_pass_rate_states(account_id,model_key,blocked) VALUES(62,'model',true);
UPDATE accounts SET extra='{"passive_quality":null}' WHERE id=63;
UPDATE passive_quality_policies SET config=config||'{"evidence_started_at":null,"evaluation_version":0}' WHERE config->>'purpose' IS NULL;`)
			require.NoError(t, err)
			if customScore {
				_, err = db.Exec(`UPDATE channel_quality_score_config SET config=jsonb_set(config,'{error_rate,weight}','65')`)
				require.NoError(t, err)
			}
			migration, err := os.ReadFile("../../migrations/317_unified_channel_quality.sql")
			require.NoError(t, err)
			_, err = db.Exec(string(migration))
			require.NoError(t, err)
			policy, err := scanPassivePolicy(db.QueryRow(`SELECT id,version,updated_at,config FROM passive_quality_policies WHERE id=$1`, legacy.ID))
			require.NoError(t, err)
			require.False(t, policy.Enabled)
			require.Empty(t, policy.GroupIDs)
			require.Equal(t, []int64{8}, policy.ConsolidatedGroupIDs)
			require.Equal(t, legacy.DecisionVersion(), policy.DecisionVersion())
			require.WithinDuration(t, legacy.EvidenceSince(), policy.EvidenceSince(), time.Microsecond)
			var archive []byte
			require.NoError(t, db.QueryRow(`SELECT config FROM quality_management_config_archive WHERE kind='passive' AND source_id=$1`, legacy.ID).Scan(&archive))
			var saved service.PassiveQualityConfig
			require.NoError(t, json.Unmarshal(archive, &saved))
			require.Equal(t, legacy.GroupIDs, saved.GroupIDs)
			require.Equal(t, legacy.ErrorRate, saved.ErrorRate)
			var stateCount int
			require.NoError(t, db.QueryRow(`SELECT count(*) FROM passive_quality_states`).Scan(&stateCount))
			require.Zero(t, stateCount)
			var extra string
			require.NoError(t, db.QueryRow(`SELECT extra::text FROM accounts WHERE id=62`).Scan(&extra))
			require.JSONEq(t, `{"keep":"yes"}`, extra)
			var window, streak int
			require.NoError(t, db.QueryRow(`SELECT (protection#>>'{rules,0,pass_rate,window_size}')::int,(protection#>>'{rules,0,pass_rate,recover_consecutive}')::int FROM scheduled_test_plans WHERE id=1`).Scan(&window, &streak))
			require.Equal(t, 1, window)
			require.Equal(t, 1, streak)
			require.NoError(t, db.QueryRow(`SELECT (protection#>>'{rules,0,pass_rate,window_size}')::int FROM scheduled_test_plans WHERE id=2`).Scan(&window))
			require.Equal(t, 10, window)
			var held bool
			require.NoError(t, db.QueryRow(`SELECT blocked FROM scheduled_test_pass_rate_states WHERE account_id=62`).Scan(&held))
			require.True(t, held)
			var weight int
			require.NoError(t, db.QueryRow(`SELECT (config#>>'{error_rate,weight}')::int FROM channel_quality_score_config`).Scan(&weight))
			if customScore {
				require.Equal(t, 65, weight)
			} else {
				require.Equal(t, 70, weight)
			}
			var threshold int
			require.NoError(t, db.QueryRow(`SELECT (config#>>'{cache_rate,pause}')::int FROM passive_quality_policies WHERE config->>'purpose'='group_quality_target'`).Scan(&threshold))
			require.Equal(t, 74, threshold, "custom targets survive upgrade")
		})
	}
}
