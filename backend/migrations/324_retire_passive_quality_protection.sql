-- Retire passive protection, not channel-status aggregation or usage billing.
-- Upgrade all application instances together. Keep a database backup for
-- rollback; configuration archives are audit evidence, not a full backup.
DO $$
BEGIN
 IF to_regclass('passive_quality_policies') IS NOT NULL THEN
  LOCK TABLE passive_quality_policies IN ACCESS EXCLUSIVE MODE;
  INSERT INTO quality_management_config_archive(kind,source_id,config)
  SELECT 'retired_passive_protection',p.id,jsonb_build_object('policy',to_jsonb(p),
   'states',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY s.account_id,s.model_key)
                     FROM passive_quality_states s WHERE s.policy_id=p.id),'[]'::jsonb))
  FROM passive_quality_policies p ON CONFLICT(kind,source_id) DO NOTHING;
 END IF;
 IF to_regclass('quality_suppliers') IS NOT NULL THEN
  INSERT INTO quality_management_config_archive(kind,source_id,config)
  SELECT 'retired_quality_supplier',s.id,jsonb_build_object('supplier',to_jsonb(s),
   'accounts',COALESCE((SELECT jsonb_agg(to_jsonb(a) ORDER BY a.account_id)
                       FROM quality_supplier_accounts a WHERE a.supplier_id=s.id),'[]'::jsonb))
  FROM quality_suppliers s ON CONFLICT(kind,source_id) DO NOTHING;
 END IF;
END $$;

-- Remove only passive-owned projections, keeping status and manual controls.
WITH changed AS (
 UPDATE accounts SET extra=extra-'passive_quality'-'group_quality_targets',updated_at=clock_timestamp()
 WHERE extra ?| ARRAY['passive_quality','group_quality_targets'] RETURNING id
) INSERT INTO scheduler_outbox(event_type,account_id) SELECT 'account_changed',id FROM changed;

INSERT INTO quality_management_config_archive(kind,source_id,config)
SELECT 'retired_recovery_budget',id,to_jsonb(c) FROM quality_operations_config c
WHERE config ? 'recovery_reserve_percent' ON CONFLICT(kind,source_id) DO NOTHING;
UPDATE quality_operations_config SET config=config-'recovery_reserve_percent',version=version+1,updated_at=clock_timestamp()
WHERE config ? 'recovery_reserve_percent';

DROP FUNCTION IF EXISTS group_quality_budget_update() CASCADE;
DO $$ BEGIN
 IF to_regclass('group_quality_traffic_leases') IS NOT NULL THEN
  DROP FUNCTION IF EXISTS group_quality_lease_charge(group_quality_traffic_leases);
 END IF;
END $$;
DROP FUNCTION IF EXISTS reject_group_target_customer_trial() CASCADE;
DROP FUNCTION IF EXISTS reject_group_target_traffic_recovery() CASCADE;
DROP TABLE IF EXISTS group_quality_report_snapshots;
DROP TABLE IF EXISTS group_quality_budget_minutes;
DROP TABLE IF EXISTS group_quality_traffic_denials;
DROP TABLE IF EXISTS group_quality_traffic_snapshots;
DROP TABLE IF EXISTS group_quality_traffic_leases;
DROP TABLE IF EXISTS passive_quality_leases;
DROP TABLE IF EXISTS passive_quality_states;
DROP TABLE IF EXISTS passive_quality_events;
DROP TABLE IF EXISTS passive_quality_failures;
DROP TABLE IF EXISTS passive_quality_cache_reports;
DROP TABLE IF EXISTS passive_quality_telemetry_gaps;
DROP TABLE IF EXISTS passive_quality_telemetry_gap_scopes;
DROP TABLE IF EXISTS passive_quality_policies;
DROP TABLE IF EXISTS quality_probe_outcomes;
DROP TABLE IF EXISTS quality_supplier_accounts;
DROP TABLE IF EXISTS quality_suppliers;

-- Statistics never call the model. Remove the whole mixed AND/OR expression
-- when it refers to a retired definition, rather than weakening its meaning.
CREATE OR REPLACE FUNCTION retired_statistics_reference(value JSONB) RETURNS BOOLEAN
LANGUAGE plpgsql STABLE AS $$
DECLARE child JSONB;
BEGIN
 IF jsonb_typeof(value)='object' THEN
  IF EXISTS(SELECT 1 FROM scheduled_test_definitions d WHERE d.output_kind='statistics'
            AND d.id::text=value->>'test_definition_id') THEN RETURN TRUE; END IF;
  FOR child IN SELECT v FROM jsonb_each(value) entry(k,v) LOOP
   IF retired_statistics_reference(child) THEN RETURN TRUE; END IF;
  END LOOP;
 ELSIF jsonb_typeof(value)='array' THEN
  FOR child IN SELECT v FROM jsonb_array_elements(value) entry(v) LOOP
   IF retired_statistics_reference(child) THEN RETURN TRUE; END IF;
  END LOOP;
 END IF;
 RETURN FALSE;
END $$;

DROP TABLE IF EXISTS pg_temp.retired_statistics_plans;
CREATE TEMP TABLE retired_statistics_plans ON COMMIT DROP AS
SELECT p.id, p.protection AS old_protection,
 ARRAY(SELECT selected FROM unnest(CASE WHEN cardinality(p.test_definition_ids)>0 THEN p.test_definition_ids
                                        WHEN p.test_definition_id IS NOT NULL THEN ARRAY[p.test_definition_id]
                                        ELSE '{}'::bigint[] END) WITH ORDINALITY ids(selected,ordinal)
       WHERE NOT EXISTS(SELECT 1 FROM scheduled_test_definitions d WHERE d.id=selected AND d.output_kind='statistics') ORDER BY ordinal) AS remaining_ids,
 COALESCE((SELECT jsonb_agg(rule-'recovery' ORDER BY ordinal)
  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules')='array' THEN p.protection->'rules' ELSE '[]'::jsonb END) WITH ORDINALITY rules(rule,ordinal)
  WHERE NOT retired_statistics_reference(rule)),'[]'::jsonb) AS remaining_rules,
 COALESCE((SELECT jsonb_agg(rule ORDER BY ordinal)
  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'combinations')='array' THEN p.protection->'combinations' ELSE '[]'::jsonb END) WITH ORDINALITY rules(rule,ordinal)
  WHERE NOT retired_statistics_reference(rule->'condition')),'[]'::jsonb) AS remaining_combinations,
 COALESCE((SELECT jsonb_agg(rule->>'id' ORDER BY ordinal)
  FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'combinations')='array' THEN p.protection->'combinations' ELSE '[]'::jsonb END) WITH ORDINALITY rules(rule,ordinal)
  WHERE retired_statistics_reference(rule->'condition')),'[]'::jsonb) AS removed_combination_ids
FROM scheduled_test_plans p
WHERE EXISTS(SELECT 1 FROM scheduled_test_definitions d WHERE d.output_kind='statistics'
              AND (d.id=p.test_definition_id OR d.id=ANY(p.test_definition_ids)))
 OR retired_statistics_reference(p.protection)
 OR EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules')='array' THEN p.protection->'rules' ELSE '[]'::jsonb END) rule WHERE rule ? 'recovery');

ALTER TABLE retired_statistics_plans ADD COLUMN no_automatic_rule BOOLEAN NOT NULL DEFAULT FALSE;
UPDATE retired_statistics_plans SET no_automatic_rule=COALESCE(old_protection->>'mode','')='all_pass'
 AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(remaining_rules) rule
  WHERE COALESCE(rule->'vote'->>'enabled','false')<>'true'
   OR rule->'auto_review'->>'enabled'='true'
   OR EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(rule->'thresholds')='array' THEN rule->'thresholds' ELSE '[]'::jsonb END) threshold WHERE threshold->>'metric'='latency_ms'));

INSERT INTO quality_management_config_archive(kind,source_id,config)
SELECT 'retired_statistics_plan',p.id,jsonb_build_object('plan',to_jsonb(p),
 'states',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY account_id,model_key,test_definition_id) FROM scheduled_test_protection_states s WHERE s.plan_id=p.id),'[]'::jsonb),
 'combinations',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY account_id,model_key) FROM scheduled_test_combination_states s WHERE s.plan_id=p.id),'[]'::jsonb))
FROM scheduled_test_plans p JOIN retired_statistics_plans r ON r.id=p.id
ON CONFLICT(kind,source_id) DO NOTHING;

DROP TABLE IF EXISTS pg_temp.retired_statistics_accounts;
CREATE TEMP TABLE retired_statistics_accounts ON COMMIT DROP AS
SELECT DISTINCT account_id FROM scheduled_test_protection_states s
WHERE s.plan_id IN(SELECT id FROM retired_statistics_plans)
   OR s.test_definition_id IN(SELECT id FROM scheduled_test_definitions WHERE output_kind='statistics')
UNION SELECT account_id FROM scheduled_test_combination_states WHERE plan_id IN(SELECT id FROM retired_statistics_plans)
UNION SELECT account_id FROM scheduled_test_pass_rate_states WHERE test_definition_id IN(SELECT id FROM scheduled_test_definitions WHERE output_kind='statistics');

UPDATE scheduled_test_plans p SET
 test_definition_ids=r.remaining_ids,
 test_definition_id=CASE WHEN p.test_definition_id=ANY(r.remaining_ids) THEN p.test_definition_id ELSE r.remaining_ids[1] END,
 enabled=p.enabled AND cardinality(r.remaining_ids)>0,
 protection=jsonb_set(jsonb_set(p.protection,'{rules}',r.remaining_rules),'{combinations}',r.remaining_combinations)
   || CASE WHEN r.remaining_rules='[]'::jsonb OR (p.protection->>'mode'='combined' AND r.remaining_combinations='[]'::jsonb)
           OR r.no_automatic_rule THEN '{"enabled":false}'::jsonb ELSE '{}'::jsonb END,
 migration_note=CASE WHEN cardinality(r.remaining_ids)=0 THEN concat_ws(' ',NULLIF(p.migration_note,''),'统计检测已退役；请添加主动智力检测项目后重新启用计划。')
  WHEN r.no_automatic_rule AND r.remaining_rules<>'[]'::jsonb
  THEN concat_ws(' ',NULLIF(p.migration_note,''),'统计检测已退役；请添加至少一项主动自动条件后重新启用质量保护。') ELSE p.migration_note END,
 updated_at=clock_timestamp()
FROM retired_statistics_plans r WHERE p.id=r.id;
UPDATE scheduled_test_definitions SET enabled=FALSE,updated_at=clock_timestamp()
WHERE output_kind='statistics' AND enabled;

-- Preserve independent capability owners of a shared combined hold.
UPDATE scheduled_test_combination_states s SET rule_ids=(
 SELECT COALESCE(jsonb_agg(rule ORDER BY ordinal),'[]'::jsonb)
 FROM jsonb_array_elements(s.rule_ids) WITH ORDINALITY rules(rule,ordinal)
 WHERE NOT(r.removed_combination_ids @> jsonb_build_array(rule)))
FROM retired_statistics_plans r
WHERE s.plan_id=r.id AND r.old_protection->>'mode'='combined'
 AND EXISTS(SELECT 1 FROM jsonb_array_elements(s.rule_ids) rule WHERE r.removed_combination_ids @> jsonb_build_array(rule));
DELETE FROM scheduled_test_combination_states s USING retired_statistics_plans r
WHERE s.plan_id=r.id AND r.old_protection->>'mode'='combined'
 AND s.rule_ids='[]'::jsonb AND r.removed_combination_ids<>'[]'::jsonb;

-- All-pass holds have no rule IDs and their trigger can predate a later
-- capability failure. Release only pure statistics holds or a confirmed fresh
-- active pass; ambiguous/missing/pending capability evidence keeps its hold
-- until the next active round, even if the original trigger was statistical.
DELETE FROM scheduled_test_combination_states c USING retired_statistics_plans r
WHERE c.plan_id=r.id AND r.old_protection->>'mode'='all_pass'
 AND (r.remaining_rules='[]'::jsonb OR (c.account_wide AND NOT r.no_automatic_rule
  AND NOT EXISTS(SELECT 1 FROM scheduled_test_protection_states s JOIN scheduled_test_definitions d ON d.id=s.test_definition_id
   WHERE s.plan_id=c.plan_id AND s.account_id=c.account_id AND s.model_key=c.model_key AND d.output_kind<>'statistics'
     AND (s.blocked OR s.verdict='fail' OR s.admin_verdict='fail'))
  AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements(r.remaining_rules) rule
   WHERE (COALESCE(rule->'vote'->>'enabled','false')<>'true'
    OR rule->'auto_review'->>'enabled'='true'
    OR EXISTS(SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(rule->'thresholds')='array' THEN rule->'thresholds' ELSE '[]'::jsonb END) threshold WHERE threshold->>'metric'='latency_ms'))
   AND NOT EXISTS(SELECT 1 FROM scheduled_test_protection_states s
    WHERE s.plan_id=c.plan_id AND s.account_id=c.account_id AND s.model_key=c.model_key
     AND s.test_definition_id::text=rule->>'test_definition_id' AND s.completed AND s.verdict='pass'
     AND s.result_started_at>=s.round_started_at
     AND EXISTS(SELECT 1 FROM scheduled_test_results result JOIN scheduled_test_plans plan ON plan.id=result.plan_id
      WHERE result.id=s.result_id AND result.plan_id=s.plan_id AND result.account_id=s.account_id AND result.model_id=s.model_key
       AND result.started_at=s.result_started_at AND result.run_id<>'' AND result.run_id=plan.latest_run_id)))));
UPDATE scheduled_test_combination_states c SET account_wide=FALSE
FROM retired_statistics_plans r WHERE c.plan_id=r.id AND c.account_wide;
DELETE FROM scheduled_test_protection_states s USING scheduled_test_definitions d
WHERE s.test_definition_id=d.id AND d.output_kind='statistics';
DELETE FROM scheduled_test_pass_rate_states s USING scheduled_test_definitions d
WHERE s.test_definition_id=d.id AND d.output_kind='statistics';
UPDATE scheduled_test_protection_states SET rule_config=rule_config-'recovery',
 recovery_phase='',recovery_cooldown_until=NULL,recovery_trial_started_at=NULL,recovery_trial_ends_at=NULL,recovery_trial_requests=0
WHERE rule_config ? 'recovery' OR recovery_phase<>'';

CREATE OR REPLACE FUNCTION reconcile_active_test_model_protection(affected_account BIGINT) RETURNS VOID
LANGUAGE plpgsql AS $$
DECLARE
 projected_holds JSONB;
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
 COALESCE((SELECT reason FROM all_holds WHERE account_wide ORDER BY updated_at DESC LIMIT 1),
          (SELECT reason FROM model_holds ORDER BY model_key LIMIT 1),'渠道质量检测保护')
 INTO projected_holds,all_models_bad,account_wide_held,protection_reason;
 paused := all_models_bad OR account_wide_held;
 SELECT COALESCE(extra,'{}'::jsonb)-'active_test_model_holds'-'active_test_all_models_paused'-'quality_protection_reason'-'quality_protection_trial'
 INTO desired_extra FROM accounts WHERE id=affected_account;
 IF projected_holds<>'{}'::jsonb THEN
  desired_extra := jsonb_set(desired_extra,'{active_test_model_holds}',projected_holds);
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
 FOR affected IN SELECT account_id FROM retired_statistics_accounts ORDER BY account_id LOOP
  PERFORM reconcile_active_test_model_protection(affected);
 END LOOP;
END $$;
DROP FUNCTION retired_statistics_reference(JSONB);

-- Keep definitions referenced by historical results and immutable evidence.
DELETE FROM scheduled_test_definitions d WHERE output_kind='statistics'
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_results r WHERE r.test_definition_id=d.id)
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_plan_definitions r WHERE r.test_definition_id=d.id)
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_pass_rate_evidence r WHERE r.test_definition_id=d.id)
 AND NOT EXISTS(SELECT 1 FROM scheduled_test_protection_events r WHERE r.test_definition_id=d.id);
