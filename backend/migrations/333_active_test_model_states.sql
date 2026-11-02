-- Persist per-model intelligence status for account lists. Keep the cascade
-- projection identical to repository reconciliation and backfill existing plans.
CREATE OR REPLACE FUNCTION reconcile_active_test_model_protection(affected_account BIGINT) RETURNS VOID
LANGUAGE plpgsql AS $$
DECLARE
 projected_holds JSONB;
 projected_states JSONB;
 all_models_bad BOOLEAN;
 account_wide_held BOOLEAN;
 protection_reason TEXT;
 desired_extra JSONB;
 paused BOOLEAN;
 changed_count BIGINT;
BEGIN
 PERFORM id FROM accounts WHERE id=affected_account FOR NO KEY UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
WITH all_holds AS (
 SELECT s.model_key,s.reason,s.updated_at,
        (s.model_key='') AS account_wide
 FROM scheduled_test_protection_states s
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=affected_account AND s.blocked AND d.output_kind<>'statistics'
 UNION ALL
 SELECT model_key,reason,updated_at,(account_wide OR model_key='')
 FROM scheduled_test_combination_states WHERE account_id=affected_account AND blocked
 UNION ALL
 SELECT model_key,COALESCE(summary->>'reason','智力检测最近通过率低于要求'),updated_at,(model_key='')
 FROM scheduled_test_pass_rate_states WHERE account_id=affected_account AND blocked
), model_holds AS (
 SELECT DISTINCT ON(model_key) model_key,reason FROM all_holds
 WHERE NOT account_wide ORDER BY model_key,updated_at DESC
), monitored_plan_models AS (
 SELECT p.id AS plan_id,p.latest_run_id,p.protection,selected.model_key
 FROM scheduled_test_plans p
 CROSS JOIN LATERAL unnest(CASE WHEN cardinality(p.model_ids)>0 THEN p.model_ids ELSE ARRAY[p.model_id] END) selected(model_key)
 WHERE p.protection->>'enabled'='true'
 AND (p.enabled OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states kept WHERE kept.plan_id=p.id AND kept.account_id=affected_account AND kept.blocked))
 AND NOT (affected_account=ANY(COALESCE(p.excluded_account_ids,'{}'::bigint[])))
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules')='array' THEN p.protection->'rules' ELSE '[]'::jsonb END) rule
   JOIN scheduled_test_definitions d ON d.id=(rule->>'test_definition_id')::bigint
   WHERE d.output_kind<>'statistics')
 AND (EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=affected_account AND ag.group_id=ANY(p.group_ids))
   OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s WHERE s.plan_id=p.id AND s.account_id=affected_account)
   OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.plan_id=p.id AND s.account_id=affected_account)
   OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.plan_id=p.id AND s.account_id=affected_account))
), monitored_models AS (
 SELECT DISTINCT model_key FROM monitored_plan_models
), model_states AS (
 SELECT m.model_key,jsonb_strip_nulls(jsonb_build_object('status',
 CASE WHEN EXISTS(SELECT 1 FROM all_holds h WHERE h.account_wide OR h.model_key=m.model_key) THEN 'blocked'
 WHEN NOT EXISTS(
  SELECT 1 FROM monitored_plan_models p
  WHERE p.model_key=m.model_key AND CASE WHEN p.protection->>'mode'='all_pass' THEN NOT EXISTS(
   SELECT 1 FROM scheduled_test_all_pass_rounds h WHERE h.plan_id=p.plan_id AND h.account_id=affected_account
   AND h.model_key=m.model_key AND h.run_id=p.latest_run_id AND h.verdict='pass'
  ) ELSE EXISTS(
   SELECT 1 FROM jsonb_array_elements(p.protection->'rules') rule
   JOIN scheduled_test_definitions d ON d.id=(rule->>'test_definition_id')::bigint AND d.output_kind<>'statistics'
   WHERE NOT EXISTS(
    SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_results r ON r.id=s.result_id
    WHERE s.plan_id=p.plan_id AND s.account_id=affected_account AND s.model_key=m.model_key AND s.test_definition_id=d.id
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
 COALESCE((SELECT jsonb_object_agg(model_key,state) FROM model_states),'{}'::jsonb)
 INTO projected_holds,all_models_bad,account_wide_held,protection_reason,projected_states;
 paused := all_models_bad OR account_wide_held;
 SELECT COALESCE(extra,'{}'::jsonb)-'active_test_model_states'-'active_test_model_holds'-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial'
 INTO desired_extra FROM accounts WHERE id=affected_account;
 IF projected_holds<>'{}'::jsonb THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_model_holds}',projected_holds);
 END IF;
 IF projected_states<>'{}'::jsonb THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_model_states}',projected_states);
 END IF;
 IF all_models_bad THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_all_models_paused}','true'::jsonb);
 END IF;
 IF paused THEN
  desired_extra := jsonb_set(desired_extra,'{quality_protection_reason}',to_jsonb(protection_reason));
 END IF;
 UPDATE accounts SET
 status=CASE WHEN schedulable AND status IN ('active','quality_paused') THEN CASE WHEN paused THEN 'quality_paused' ELSE 'active' END ELSE status END,
 extra=desired_extra,updated_at=clock_timestamp()
 WHERE id=affected_account AND deleted_at IS NULL
 AND (extra IS DISTINCT FROM desired_extra OR (schedulable AND status IN ('active','quality_paused') AND status<>CASE WHEN paused THEN 'quality_paused' ELSE 'active' END));
 GET DIAGNOSTICS changed_count = ROW_COUNT;
 IF changed_count>0 THEN
  INSERT INTO scheduler_outbox(event_type,account_id,dedup_key)
  VALUES('account_changed',affected_account,'scheduler_outbox:' || encode(sha256(convert_to('account_changed','UTF8') || decode('00','hex') || convert_to(affected_account::text,'UTF8') || decode('0000','hex')),'hex'))
  ON CONFLICT(dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING;
 END IF;
END;
$$;
DO $$ DECLARE affected BIGINT;
BEGIN
 FOR affected IN
  SELECT a.id FROM accounts a WHERE a.deleted_at IS NULL AND (
   a.extra ?| ARRAY['active_test_model_holds','active_test_all_models_paused','active_test_model_states']
   OR EXISTS(SELECT 1 FROM scheduled_test_plans p WHERE p.protection->>'enabled'='true' AND p.enabled
    AND EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=a.id AND ag.group_id=ANY(p.group_ids)))
   OR EXISTS(SELECT 1 FROM scheduled_test_protection_states s WHERE s.account_id=a.id)
   OR EXISTS(SELECT 1 FROM scheduled_test_combination_states s WHERE s.account_id=a.id)
   OR EXISTS(SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.account_id=a.id))
  ORDER BY a.id
 LOOP
  PERFORM reconcile_active_test_model_protection(affected);
 END LOOP;
END $$;
