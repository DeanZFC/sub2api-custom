package repository

import (
	"context"
	"database/sql"
	"encoding/json"

	"github.com/Wei-Shaw/sub2api/internal/service"
	"github.com/lib/pq"
)

// The model set comes from the configured policy, not only completed results:
// the first failing model in a new round must not pause healthy/pending peers.
// Existing pass-rate holds survive disabling the schedule, as before.
const activeTestProtectionProjectionSQL = `WITH all_holds AS (
 SELECT s.model_key,s.reason,s.updated_at,
        (s.model_key='') AS account_wide
 FROM scheduled_test_protection_states s
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=$1 AND s.blocked AND d.output_kind<>'statistics'
 UNION ALL
 SELECT model_key,reason,updated_at,(account_wide OR model_key='')
 FROM scheduled_test_combination_states WHERE account_id=$1 AND blocked
 UNION ALL
 SELECT model_key,COALESCE(summary->>'reason','智力检测最近通过率低于要求'),updated_at,(model_key='')
 FROM scheduled_test_pass_rate_states WHERE account_id=$1 AND blocked
), model_holds AS (
 SELECT DISTINCT ON(model_key) model_key,reason FROM all_holds
 WHERE NOT account_wide ORDER BY model_key,updated_at DESC
), monitored_plan_models AS (
 SELECT p.id AS plan_id,p.latest_run_id,p.protection,selected.model_key
 FROM scheduled_test_plans p
 CROSS JOIN LATERAL unnest(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END) selected(model_key)
 WHERE p.protection->>'enabled'='true'
 AND (p.enabled OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states kept WHERE kept.plan_id=p.id AND kept.account_id=$1 AND kept.blocked))
 AND NOT ($1=ANY(COALESCE(p.excluded_account_ids,'{}'::bigint[])))
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules')='array' THEN p.protection->'rules' ELSE '[]'::jsonb END) rule
   JOIN scheduled_test_definitions d ON d.id=(rule->>'test_definition_id')::bigint
   WHERE d.output_kind<>'statistics')
 AND (EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=$1 AND ag.group_id=ANY(p.group_ids))
   OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s WHERE s.plan_id=p.id AND s.account_id=$1)
   OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.plan_id=p.id AND s.account_id=$1)
   OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.plan_id=p.id AND s.account_id=$1))
), monitored_models AS (
 SELECT DISTINCT model_key FROM monitored_plan_models
), model_states AS (
 SELECT m.model_key,jsonb_strip_nulls(jsonb_build_object('status',
 CASE WHEN EXISTS(SELECT 1 FROM all_holds h WHERE h.account_wide OR h.model_key=m.model_key) THEN 'blocked'
 WHEN NOT EXISTS(
  SELECT 1 FROM monitored_plan_models p
  WHERE p.model_key=m.model_key AND CASE WHEN p.protection->>'mode'='all_pass' THEN NOT EXISTS(
   SELECT 1 FROM scheduled_test_all_pass_rounds h WHERE h.plan_id=p.plan_id AND h.account_id=$1
   AND h.model_key=m.model_key AND h.run_id=p.latest_run_id AND h.verdict='pass'
  ) ELSE EXISTS(
   SELECT 1 FROM jsonb_array_elements(p.protection->'rules') rule
   JOIN scheduled_test_definitions d ON d.id=(rule->>'test_definition_id')::bigint AND d.output_kind<>'statistics'
   WHERE NOT EXISTS(
    SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_results r ON r.id=s.result_id
    WHERE s.plan_id=p.plan_id AND s.account_id=$1 AND s.model_key=m.model_key AND s.test_definition_id=d.id
    AND s.completed AND s.verdict='pass' AND s.result_started_at>=s.round_started_at
    AND r.started_at=s.result_started_at AND r.run_id<>'' AND r.run_id=p.latest_run_id AND NOT r.protection_revoked
   )
  ) END
 ) THEN 'normal' ELSE 'pending' END,
 'reason',(SELECT h.reason FROM all_holds h WHERE h.account_wide OR h.model_key=m.model_key ORDER BY h.updated_at DESC LIMIT 1))) AS state
 FROM (SELECT model_key FROM monitored_models UNION SELECT model_key FROM model_holds) m
)
SELECT COALESCE((SELECT jsonb_object_agg(model_key,reason) FROM model_holds),'{}'::jsonb),
 EXISTS(SELECT 1 FROM monitored_models) AND NOT EXISTS(
   SELECT 1 FROM monitored_models m WHERE NOT EXISTS(SELECT 1 FROM model_holds h WHERE h.model_key=m.model_key OR h.model_key='')),
 EXISTS(SELECT 1 FROM all_holds WHERE account_wide),
 COALESCE((SELECT reason FROM all_holds WHERE account_wide ORDER BY updated_at DESC LIMIT 1),
          (SELECT reason FROM model_holds ORDER BY model_key LIMIT 1),'渠道质量检测保护'),
 COALESCE((SELECT jsonb_object_agg(model_key,state) FROM model_states),'{}'::jsonb)`

type activeTestProtectionProjection struct {
	modelHolds       json.RawMessage
	modelStates      json.RawMessage
	allModelsBlocked bool
	accountWide      bool
	reason           string
}

func readActiveTestProtection(ctx context.Context, tx sqlExecutor, accountID int64) (*activeTestProtectionProjection, error) {
	out := &activeTestProtectionProjection{}
	err := scanSingleRow(ctx, tx, activeTestProtectionProjectionSQL, []any{accountID}, &out.modelHolds, &out.allModelsBlocked, &out.accountWide, &out.reason, &out.modelStates)
	return out, err
}

func reconcileProtectionAccount(ctx context.Context, tx sqlExecutor, accountID int64) error {
	projection, err := readActiveTestProtection(ctx, tx, accountID)
	if err != nil {
		return err
	}
	paused := projection.allModelsBlocked || projection.accountWide
	// Keep manual account state/scheduling switches intact while refreshing the
	// model projection, so later administrative re-enablement still sees it.
	changed, err := tx.ExecContext(ctx, `WITH desired AS (
 SELECT CASE WHEN $2 THEN 'quality_paused' ELSE 'active' END AS status,
 CASE WHEN $2 THEN jsonb_set(base,'{quality_protection_reason}',to_jsonb($3::text))
      ELSE base END AS extra
 FROM (SELECT CASE WHEN $6::jsonb='{}'::jsonb THEN paused_models-'active_test_model_states'
              ELSE jsonb_set(paused_models,'{active_test_model_states}',$6::jsonb) END AS base
 FROM (SELECT CASE WHEN $5 THEN jsonb_set(models,'{active_test_all_models_paused}','true'::jsonb) ELSE models END AS paused_models
 FROM (SELECT CASE WHEN $4::jsonb='{}'::jsonb THEN COALESCE(extra,'{}'::jsonb)-'active_test_model_holds'-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial'
       ELSE jsonb_set(COALESCE(extra,'{}'::jsonb)-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial','{active_test_model_holds}',$4::jsonb) END AS models
       FROM accounts WHERE id=$1) model_projection) paused_projection) clean
 ) UPDATE accounts a SET
 status=CASE WHEN a.schedulable AND a.status IN ('active','quality_paused') THEN d.status ELSE a.status END,
 extra=d.extra,updated_at=clock_timestamp()
 FROM desired d WHERE a.id=$1 AND a.deleted_at IS NULL
 AND (a.extra IS DISTINCT FROM d.extra OR (a.schedulable AND a.status IN ('active','quality_paused') AND a.status<>d.status))`, accountID, paused, projection.reason, []byte(projection.modelHolds), projection.allModelsBlocked, []byte(projection.modelStates))
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, "account_changed", &accountID, nil, nil)
}

// Include the old and new detection scopes, even when a healthy plan has never
// produced a state. Adding/removing that plan changes which models must fail
// before another policy's existing holds suspend the whole account.
func scheduledTestProjectionAccountIDs(ctx context.Context, tx *sql.Tx, plans ...*service.ScheduledTestPlan) ([]int64, error) {
	groups, planIDs := map[int64]bool{}, map[int64]bool{}
	for _, plan := range plans {
		if plan == nil {
			continue
		}
		planIDs[plan.ID] = true
		for _, id := range plan.GroupIDs {
			groups[id] = true
		}
	}
	rows, err := tx.QueryContext(ctx, `SELECT a.id FROM accounts a WHERE a.deleted_at IS NULL AND (
 EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY($1))
 OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s WHERE s.account_id=a.id AND s.plan_id=ANY($2))
 OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.account_id=a.id AND s.plan_id=ANY($2))
 OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=a.id AND s.plan_id=ANY($2)))
 ORDER BY a.id FOR NO KEY UPDATE`, pq.Array(sortedProtectionIDs(groups)), pq.Array(sortedProtectionIDs(planIDs)))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var ids []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		ids = append(ids, id)
	}
	return ids, rows.Err()
}

func reconcileScheduledTestProjectionAccounts(ctx context.Context, tx sqlExecutor, ids []int64) error {
	for _, id := range ids {
		if err := reconcileProtectionAccount(ctx, tx, id); err != nil {
			return err
		}
	}
	return nil
}
