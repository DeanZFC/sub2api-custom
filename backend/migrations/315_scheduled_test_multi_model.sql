-- Preserve existing single-model plans while allowing one round to test every
-- selected model. Each model owns its evaluation and recovery state; scheduling
-- and group actions continue to apply to the account.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS model_ids TEXT[] NOT NULL DEFAULT '{}';
UPDATE scheduled_test_plans SET model_ids=ARRAY[model_id] WHERE cardinality(model_ids)=0 AND model_id<>'';

ALTER TABLE scheduled_test_protection_states
    ADD COLUMN IF NOT EXISTS model_key TEXT NOT NULL DEFAULT '';
UPDATE scheduled_test_protection_states s
SET model_key=COALESCE((SELECT r.model_id FROM scheduled_test_results r WHERE r.id=s.result_id),p.model_id)
FROM scheduled_test_plans p WHERE p.id=s.plan_id AND s.model_key='';
ALTER TABLE scheduled_test_protection_states DROP CONSTRAINT IF EXISTS scheduled_test_protection_states_pkey;
ALTER TABLE scheduled_test_protection_states
    ADD PRIMARY KEY (plan_id,account_id,test_definition_id,model_key);

ALTER TABLE scheduled_test_combination_states
    ADD COLUMN IF NOT EXISTS model_key TEXT NOT NULL DEFAULT '';
UPDATE scheduled_test_combination_states s SET model_key=p.model_id
FROM scheduled_test_plans p WHERE p.id=s.plan_id AND s.model_key='';
ALTER TABLE scheduled_test_combination_states DROP CONSTRAINT IF EXISTS scheduled_test_combination_states_pkey;
ALTER TABLE scheduled_test_combination_states
    ADD PRIMARY KEY (plan_id,account_id,model_key);

-- A statistics failure keeps the existing whole-account recovery contract.
-- Capability failures are isolated to the model that was evaluated.
ALTER TABLE scheduled_test_combination_states
    ADD COLUMN IF NOT EXISTS account_wide BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE scheduled_test_combination_states c SET account_wide=TRUE
WHERE c.blocked AND EXISTS (
    SELECT 1 FROM scheduled_test_protection_states s
    JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
    WHERE s.plan_id=c.plan_id AND s.account_id=c.account_id AND s.model_key=c.model_key
      AND d.output_kind='statistics' AND (s.verdict='fail' OR s.blocked)
);

-- Cascading deletes must update the scheduler projection in the same
-- transaction, including when no Go repository method performs the delete.
CREATE FUNCTION reconcile_active_test_model_protection(affected_account BIGINT) RETURNS VOID
LANGUAGE plpgsql AS $$
DECLARE
 projected_holds JSONB;
 all_models_bad BOOLEAN;
 account_wide_held BOOLEAN;
 trial_allowed BOOLEAN;
 protection_reason TEXT;
 desired_extra JSONB;
 paused BOOLEAN;
 in_trial BOOLEAN;
 changed_count BIGINT;
BEGIN
 PERFORM id FROM accounts WHERE id=affected_account FOR NO KEY UPDATE;
 IF NOT FOUND THEN RETURN; END IF;
WITH all_holds AS (
 SELECT s.model_key,s.reason,s.updated_at,
        (d.output_kind='statistics' OR s.model_key='') AS account_wide,
        (s.recovery_phase='trial' AND s.rule_config->'recovery'->>'enabled'='true'
         AND s.recovery_trial_started_at<=NOW() AND s.recovery_trial_ends_at>NOW()) AS trial
 FROM scheduled_test_protection_states s
 JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
 WHERE s.account_id=affected_account AND s.blocked
 UNION ALL
 SELECT model_key,reason,updated_at,(account_wide OR model_key=''),FALSE
 FROM scheduled_test_combination_states WHERE account_id=affected_account AND blocked
 UNION ALL
 SELECT model_key,COALESCE(summary->>'reason','智力检测最近通过率低于要求'),updated_at,(model_key=''),FALSE
 FROM scheduled_test_pass_rate_states WHERE account_id=affected_account AND blocked
), model_holds AS (
 SELECT DISTINCT ON(model_key) model_key,reason FROM all_holds
 WHERE NOT account_wide ORDER BY model_key,updated_at DESC
), monitored_models AS (
 SELECT DISTINCT selected.model_key
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
)
SELECT COALESCE((SELECT jsonb_object_agg(model_key,reason) FROM model_holds),'{}'::jsonb),
 EXISTS(SELECT 1 FROM monitored_models) AND NOT EXISTS(
   SELECT 1 FROM monitored_models m WHERE NOT EXISTS(SELECT 1 FROM model_holds h WHERE h.model_key=m.model_key OR h.model_key='')),
 EXISTS(SELECT 1 FROM all_holds WHERE account_wide),
 COALESCE((SELECT bool_and(COALESCE(trial,FALSE)) FROM all_holds WHERE account_wide),FALSE),
 COALESCE((SELECT reason FROM all_holds WHERE account_wide ORDER BY updated_at DESC LIMIT 1),
          (SELECT reason FROM model_holds ORDER BY model_key LIMIT 1),'渠道质量检测保护')
 INTO projected_holds,all_models_bad,account_wide_held,trial_allowed,protection_reason;
 paused := all_models_bad OR (account_wide_held AND NOT trial_allowed);
 in_trial := account_wide_held AND trial_allowed AND NOT all_models_bad;
 SELECT COALESCE(extra,'{}'::jsonb)-'active_test_model_holds'-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial'
 INTO desired_extra FROM accounts WHERE id=affected_account;
 IF projected_holds<>'{}'::jsonb THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_model_holds}',projected_holds);
 END IF;
 IF all_models_bad THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_all_models_paused}','true'::jsonb);
 END IF;
 IF paused OR in_trial THEN
  desired_extra := jsonb_set(desired_extra,'{quality_protection_reason}',to_jsonb(protection_reason));
 END IF;
 IF in_trial THEN desired_extra := jsonb_set(desired_extra,'{quality_protection_trial}','true'::jsonb); END IF;
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
CREATE FUNCTION reconcile_deleted_scheduled_test_model_holds() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE affected_account BIGINT;
BEGIN
 FOR affected_account IN SELECT DISTINCT account_id FROM deleted_model_states ORDER BY account_id LOOP
  PERFORM reconcile_active_test_model_protection(affected_account);
 END LOOP;
 RETURN NULL;
END;
$$;
CREATE TRIGGER scheduled_test_protection_deleted_model_projection
AFTER DELETE ON scheduled_test_protection_states
REFERENCING OLD TABLE AS deleted_model_states
FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_model_holds();
CREATE TRIGGER scheduled_test_combination_deleted_model_projection
AFTER DELETE ON scheduled_test_combination_states
REFERENCING OLD TABLE AS deleted_model_states
FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_model_holds();
CREATE TRIGGER scheduled_test_pass_rate_deleted_model_projection
AFTER DELETE ON scheduled_test_pass_rate_states
REFERENCING OLD TABLE AS deleted_model_states
FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_model_holds();

-- Refresh existing accounts once so old holds receive the new model marker.
DO $$ DECLARE affected_account BIGINT;
BEGIN
 FOR affected_account IN
  SELECT account_id FROM scheduled_test_protection_states
  UNION SELECT account_id FROM scheduled_test_combination_states
  UNION SELECT account_id FROM scheduled_test_pass_rate_states ORDER BY account_id
 LOOP
  PERFORM reconcile_active_test_model_protection(affected_account);
 END LOOP;
END $$;

-- Share the ordinary scheduler outbox deduplication key across both projections.
CREATE OR REPLACE FUNCTION reconcile_deleted_scheduled_test_pass_rate_holds() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 affected_account BIGINT;
 remaining_holds JSONB;
 changed_count BIGINT;
BEGIN
 FOR affected_account IN SELECT DISTINCT account_id FROM deleted_pass_rate_states ORDER BY account_id LOOP
  PERFORM id FROM accounts WHERE id=affected_account FOR NO KEY UPDATE;
  IF NOT FOUND THEN
   CONTINUE;
  END IF;
  SELECT COALESCE(jsonb_object_agg(model_key,reason),'{}'::jsonb) INTO remaining_holds FROM (
   SELECT DISTINCT ON(model_key) model_key,COALESCE(summary->>'reason','recovery_pending') AS reason
   FROM scheduled_test_pass_rate_states WHERE account_id=affected_account AND blocked
   ORDER BY model_key,updated_at DESC
  ) holds;
  UPDATE accounts SET extra=CASE WHEN remaining_holds='{}'::jsonb
    THEN COALESCE(extra,'{}'::jsonb)-'active_pass_rate_holds'
    ELSE jsonb_set(COALESCE(extra,'{}'::jsonb),'{active_pass_rate_holds}',remaining_holds) END,
   updated_at=clock_timestamp()
  WHERE id=affected_account AND COALESCE(extra->'active_pass_rate_holds','{}'::jsonb) IS DISTINCT FROM remaining_holds;
  GET DIAGNOSTICS changed_count = ROW_COUNT;
  IF changed_count > 0 THEN
   INSERT INTO scheduler_outbox(event_type,account_id,dedup_key)
  VALUES('account_changed',affected_account,'scheduler_outbox:' || encode(sha256(convert_to('account_changed','UTF8') || decode('00','hex') || convert_to(affected_account::text,'UTF8') || decode('0000','hex')),'hex'))
  ON CONFLICT(dedup_key) WHERE dedup_key IS NOT NULL DO NOTHING;
  END IF;
 END LOOP;
 RETURN NULL;
END;
$$;
