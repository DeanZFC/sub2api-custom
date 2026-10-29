//go:build integration

package repository

import (
	"context"
	"testing"

	dbmigrations "github.com/Wei-Shaw/sub2api/migrations"
	"github.com/lib/pq"
	"github.com/stretchr/testify/require"
)

func TestMigration320RetiresOnlyKnowledgeCutoff(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	migrationSQL, err := dbmigrations.FS.ReadFile("320_retire_knowledge_cutoff.sql")
	require.NoError(t, err)

	// Simulate an installation that applied 319 before receiving this rollback.
	seedSQL, err := dbmigrations.FS.ReadFile("319_scheduled_test_knowledge_cutoff.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(seedSQL))
	require.NoError(t, err)
	var seeded int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_definitions WHERE key='knowledge_cutoff' AND enabled`).Scan(&seeded))
	require.Equal(t, 1, seeded)
	for range 2 {
		_, err = tx.ExecContext(ctx, string(migrationSQL))
		require.NoError(t, err)
	}
	var retained int
	var candyEnabled bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_definitions WHERE key='knowledge_cutoff'`).Scan(&retained))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT enabled FROM scheduled_test_definitions WHERE key='candy'`).Scan(&candyEnabled))
	require.Zero(t, retained)
	require.True(t, candyEnabled)

	// Audit evidence on upgraded installations must retain its definition.
	_, err = tx.ExecContext(ctx, string(seedSQL))
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_protection_events (plan_id,account_id,test_definition_id,result_id,snapshot)
		SELECT 0,0,id,0,'{}'::jsonb FROM scheduled_test_definitions WHERE key='knowledge_cutoff'`)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	var disabled bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT NOT enabled FROM scheduled_test_definitions WHERE key='knowledge_cutoff'`).Scan(&disabled))
	require.True(t, disabled)
}

func TestMigration320RemovesKnowledgeCutoffFromPlans(t *testing.T) {
	tx := testTx(t)
	ctx := context.Background()
	seedSQL, err := dbmigrations.FS.ReadFile("319_scheduled_test_knowledge_cutoff.sql")
	require.NoError(t, err)
	migrationSQL, err := dbmigrations.FS.ReadFile("320_retire_knowledge_cutoff.sql")
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(seedSQL))
	require.NoError(t, err)
	var retiredID, candyID, mixedPlanID, onlyPlanID, combinedPlanID int64
	var groupID int64
	var accountID int64
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='knowledge_cutoff'`).Scan(&retiredID))
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT id FROM scheduled_test_definitions WHERE key='candy'`).Scan(&candyID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO groups(name) VALUES ('retire-cutoff-test') RETURNING id`).Scan(&groupID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO accounts(name,platform,type)
		VALUES ('retire-cutoff-account','openai','apikey') RETURNING id`).Scan(&accountID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans
		(name,group_id,group_ids,target_mode,model_id,test_definition_id,test_definition_ids,protection)
		VALUES ('mixed',$3::bigint,ARRAY[$3::bigint],'all_accounts','gpt-5.4',$1::bigint,ARRAY[$1::bigint,$2::bigint],
		jsonb_build_object('enabled',true,'rules',jsonb_build_array(
		jsonb_build_object('test_definition_id',$1::bigint),jsonb_build_object('test_definition_id',$2::bigint)))) RETURNING id`,
		retiredID, candyID, groupID).Scan(&mixedPlanID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans
		(name,group_id,group_ids,target_mode,model_id,test_definition_id,test_definition_ids,protection)
		VALUES ('only',$2::bigint,ARRAY[$2::bigint],'all_accounts','gpt-5.4',$1::bigint,ARRAY[$1::bigint],
		jsonb_build_object('enabled',true,'rules',jsonb_build_array(
		jsonb_build_object('test_definition_id',$1::bigint)))) RETURNING id`, retiredID, groupID).Scan(&onlyPlanID))
	require.NoError(t, tx.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans
		(name,group_id,group_ids,target_mode,model_id,test_definition_id,test_definition_ids,protection)
		VALUES ('combined',$3::bigint,ARRAY[$3::bigint],'all_accounts','gpt-5.4',$1::bigint,ARRAY[$1::bigint,$2::bigint],
		jsonb_build_object('enabled',true,'mode','combined',
		'rules',jsonb_build_array(jsonb_build_object('test_definition_id',$1::bigint),jsonb_build_object('test_definition_id',$2::bigint)),
		'combinations',jsonb_build_array(
			jsonb_build_object('id','retired','condition',jsonb_build_object('operator','all','conditions',jsonb_build_array(jsonb_build_object('operator','test','test_definition_id',$1::bigint))), 'action','{}'::jsonb),
			jsonb_build_object('id','candy','condition',jsonb_build_object('operator','test','test_definition_id',$2::bigint), 'action','{}'::jsonb)))) RETURNING id`,
		retiredID, candyID, groupID).Scan(&combinedPlanID))
	_, err = tx.ExecContext(ctx, `INSERT INTO scheduled_test_combination_states
		(plan_id,account_id,model_key,rule_ids,blocked) VALUES
		($1,$2,'retired-model','["retired"]'::jsonb,false),
		($1,$2,'candy-model','["candy"]'::jsonb,false)`, combinedPlanID, accountID)
	require.NoError(t, err)
	_, err = tx.ExecContext(ctx, string(migrationSQL))
	require.NoError(t, err)
	var mixedSelected, onlySelected []int64
	var mixedLegacyID int64
	var mixedEnabled, onlyEnabled, mixedProtectionEnabled, onlyProtectionEnabled bool
	var mixedRules int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT test_definition_ids,test_definition_id,enabled,
		(protection->>'enabled')::boolean,jsonb_array_length(protection->'rules')
		FROM scheduled_test_plans WHERE id=$1`, mixedPlanID).Scan(
		pq.Array(&mixedSelected), &mixedLegacyID, &mixedEnabled, &mixedProtectionEnabled, &mixedRules))
	require.Equal(t, []int64{candyID}, mixedSelected)
	require.Equal(t, candyID, mixedLegacyID)
	require.True(t, mixedEnabled)
	require.True(t, mixedProtectionEnabled)
	require.Equal(t, 1, mixedRules)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT test_definition_ids,enabled,(protection->>'enabled')::boolean
		FROM scheduled_test_plans WHERE id=$1`, onlyPlanID).Scan(
		pq.Array(&onlySelected), &onlyEnabled, &onlyProtectionEnabled))
	require.Empty(t, onlySelected)
	require.False(t, onlyEnabled)
	require.False(t, onlyProtectionEnabled)
	var remainingCombinationID string
	var remainingTestID int64
	var remainingCombinationCount int
	var combinedProtectionEnabled bool
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT jsonb_array_length(protection->'combinations'),
		protection->'combinations'->0->>'id',(protection->'combinations'->0->'condition'->>'test_definition_id')::bigint,
		(protection->>'enabled')::boolean FROM scheduled_test_plans WHERE id=$1`, combinedPlanID).Scan(
		&remainingCombinationCount, &remainingCombinationID, &remainingTestID, &combinedProtectionEnabled))
	require.Equal(t, 1, remainingCombinationCount)
	require.Equal(t, "candy", remainingCombinationID)
	require.Equal(t, candyID, remainingTestID)
	require.True(t, combinedProtectionEnabled)
	var keptCombinations int
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_combination_states
		WHERE plan_id=$1 AND model_key='candy-model' AND rule_ids='["candy"]'::jsonb`, combinedPlanID).Scan(&keptCombinations))
	require.Equal(t, 1, keptCombinations)
	require.NoError(t, tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM scheduled_test_combination_states
		WHERE plan_id=$1 AND model_key='retired-model'`, combinedPlanID).Scan(&keptCombinations))
	require.Zero(t, keptCombinations)
}
