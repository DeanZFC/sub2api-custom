package repository

import (
	"context"
	"encoding/json"
)

// The model set comes from the configured policy, not only completed results:
// the first failing model in a new round must not pause healthy/pending peers.
// Existing pass-rate holds survive disabling the schedule, as before.
const activeTestProtectionProjectionSQL = `WITH all_holds AS (
 SELECT s.model_key,s.reason,s.updated_at,
        (s.model_key='') AS account_wide
 FROM scheduled_test_protection_states s
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=$1 AND s.blocked
 UNION ALL
 SELECT model_key,reason,updated_at,(account_wide OR model_key='')
 FROM scheduled_test_combination_states WHERE account_id=$1 AND blocked
 UNION ALL
 SELECT model_key,COALESCE(summary->>'reason','智力检测最近通过率低于要求'),updated_at,(model_key='')
 FROM scheduled_test_pass_rate_states WHERE account_id=$1 AND blocked
), model_holds AS (
 SELECT DISTINCT ON(model_key) model_key,reason FROM all_holds
 WHERE NOT account_wide ORDER BY model_key,updated_at DESC
), monitored_models AS (
 SELECT DISTINCT selected.model_key
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
)
SELECT COALESCE((SELECT jsonb_object_agg(model_key,reason) FROM model_holds),'{}'::jsonb),
 EXISTS(SELECT 1 FROM monitored_models) AND NOT EXISTS(
   SELECT 1 FROM monitored_models m WHERE NOT EXISTS(SELECT 1 FROM model_holds h WHERE h.model_key=m.model_key OR h.model_key='')),
 EXISTS(SELECT 1 FROM all_holds WHERE account_wide),
 COALESCE((SELECT reason FROM all_holds WHERE account_wide ORDER BY updated_at DESC LIMIT 1),
          (SELECT reason FROM model_holds ORDER BY model_key LIMIT 1),'渠道质量检测保护')`

type activeTestProtectionProjection struct {
	modelHolds       json.RawMessage
	allModelsBlocked bool
	accountWide      bool
	reason           string
}

func readActiveTestProtection(ctx context.Context, tx sqlExecutor, accountID int64) (*activeTestProtectionProjection, error) {
	out := &activeTestProtectionProjection{}
	err := scanSingleRow(ctx, tx, activeTestProtectionProjectionSQL, []any{accountID}, &out.modelHolds, &out.allModelsBlocked, &out.accountWide, &out.reason)
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
 FROM (SELECT CASE WHEN $5 THEN jsonb_set(models,'{active_test_all_models_paused}','true'::jsonb) ELSE models END AS base
 FROM (SELECT CASE WHEN $4::jsonb='{}'::jsonb THEN COALESCE(extra,'{}'::jsonb)-'active_test_model_holds'-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial'
       ELSE jsonb_set(COALESCE(extra,'{}'::jsonb)-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial','{active_test_model_holds}',$4::jsonb) END AS models
       FROM accounts WHERE id=$1) model_projection) clean
 ) UPDATE accounts a SET
 status=CASE WHEN a.schedulable AND a.status IN ('active','quality_paused') THEN d.status ELSE a.status END,
 extra=d.extra,updated_at=clock_timestamp()
 FROM desired d WHERE a.id=$1 AND a.deleted_at IS NULL
 AND (a.extra IS DISTINCT FROM d.extra OR (a.schedulable AND a.status IN ('active','quality_paused') AND a.status<>d.status))`, accountID, paused, projection.reason, []byte(projection.modelHolds), projection.allModelsBlocked)
	if err != nil {
		return err
	}
	count, err := changed.RowsAffected()
	if err != nil || count == 0 {
		return err
	}
	return enqueueSchedulerOutbox(ctx, tx, "account_changed", &accountID, nil, nil)
}
