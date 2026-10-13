//go:build integration

package repository

import (
	"context"
	"database/sql"
	"encoding/json"
	"testing"
	"time"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/stretchr/testify/require"
)

func testScheduledTestProtectionDetails(t *testing.T, ctx context.Context, db *sql.DB, results *scheduledTestResultRepository, definitionID int64) {
	now := time.Now().UTC().Truncate(time.Microsecond)
	exec := func(query string, args ...any) {
		t.Helper()
		_, err := db.ExecContext(ctx, query, args...)
		require.NoError(t, err)
	}
	exec(`INSERT INTO accounts (id,name,status,schedulable) VALUES (901,'Pause details','quality_paused',false),(902,'Other account','quality_paused',true)`)
	rule := service.ScheduledTestProtectionRule{TestDefinitionID: definitionID, Thresholds: []service.ScheduledTestThreshold{{Metric: "output_numeric", Operator: "lt", Value: 30}}}
	config := service.ScheduledTestProtectionConfig{Enabled: true, Mode: "combined", Rules: []service.ScheduledTestProtectionRule{rule}, Combinations: []service.ScheduledTestCombinationRule{
		{ID: "failed", Name: "不通过", Condition: service.ScheduledTestCombinationCondition{Operator: "test", TestDefinitionID: definitionID, Verdict: "fail"}},
	}}
	configRaw, err := json.Marshal(config)
	require.NoError(t, err)
	ruleRaw, err := json.Marshal(rule)
	require.NoError(t, err)
	var planID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO scheduled_test_plans(name,target_mode,group_id,group_ids,latest_run_id,protection) VALUES('Tooltip strategy','all_accounts',8,'{8}','run-a',$1) RETURNING id`, string(configRaw)).Scan(&planID))
	var resultID int64
	require.NoError(t, db.QueryRowContext(ctx, `INSERT INTO scheduled_test_results(plan_id,account_id,test_definition_id,status,output_kind,response_text,output_numeric,started_at,run_id)
 VALUES($1,901,$2,'success','number','29',1,$3,'run-a') RETURNING id`, planID, definitionID, now).Scan(&resultID))
	exec(`INSERT INTO scheduled_test_protection_states(plan_id,account_id,test_definition_id,result_id,result_started_at,round_started_at,generation,automated_verdict,verdict,reason,blocked,completed,rule_config)
 VALUES($1,901,$2,$3,$4,$4,2,'fail','fail','输出数值小于30（实际 29）',true,true,$5)`, planID, definitionID, resultID, now, string(ruleRaw))
	exec(`INSERT INTO scheduled_test_combination_states(plan_id,account_id,blocked,reason,rule_ids,updated_at)
 VALUES($1,901,true,'命中组合规则：不通过','["failed"]',$2),($1,902,true,'Other hold','["failed"]',$2)`, planID, now)
	exec(`INSERT INTO users(id) VALUES(901),(902)`)
	exec(`INSERT INTO scheduled_test_votes(result_id,user_id,generation,vote) VALUES($1,901,2,'fail'),($1,902,1,'fail')`, resultID)
	svc := service.NewScheduledTestService(nil, results)
	holds, err := svc.AccountProtectionDetails(ctx, 901)
	require.NoError(t, err)
	require.Len(t, holds, 2, "a manual scheduling stop must not hide existing quality holds")
	require.Equal(t, "combined", holds[0].Mode)
	require.Equal(t, "per_test", holds[1].Mode)
	require.Equal(t, "pass", holds[0].Rules[0].CurrentMatch)
	require.Equal(t, int64(1), holds[0].Tests[0].FailCount, "votes from the previous generation are excluded")
	require.Equal(t, float64(29), *holds[0].Tests[0].Thresholds[0].Observed, "numeric output is normalized from the stored response")
	require.Contains(t, holds[0].Tests[0].AutomaticReason, "实际 29")

	// The result still exists, but a new run invalidates its verdict.
	exec(`UPDATE scheduled_test_plans SET latest_run_id='run-b' WHERE id=$1`, planID)
	exec(`UPDATE scheduled_test_protection_states SET round_started_at=$2 WHERE plan_id=$1`, planID, now.Add(time.Minute))
	holds, err = svc.AccountProtectionDetails(ctx, 901)
	require.NoError(t, err)
	require.Len(t, holds, 2)
	require.True(t, holds[0].PriorRound)
	require.Equal(t, "pending", holds[0].Rules[0].CurrentMatch)
	require.False(t, holds[0].Tests[0].Current)
	require.Nil(t, holds[0].Tests[0].Thresholds[0].Observed)
	require.Zero(t, holds[0].Tests[0].FailCount)

	// A pruned source leaves the hold and original rule visible, without a
	// fabricated result ID or current measurements.
	exec(`DELETE FROM scheduled_test_results WHERE id=$1`, resultID)
	holds, err = svc.AccountProtectionDetails(ctx, 901)
	require.NoError(t, err)
	require.Zero(t, holds[0].Tests[0].ResultID)
	require.Equal(t, "不通过", holds[0].Rules[0].Name)
	exec(`UPDATE accounts SET status='active' WHERE id=901`)
	holds, err = svc.AccountProtectionDetails(ctx, 901)
	require.NoError(t, err)
	require.Empty(t, holds)
	holds, err = svc.AccountProtectionDetails(ctx, 902)
	require.NoError(t, err)
	require.Len(t, holds, 1, "other accounts are not affected")
}
