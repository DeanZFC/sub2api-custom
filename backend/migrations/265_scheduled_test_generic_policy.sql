-- Replace the dedicated two-tier workflow with administrator-configured rules.
-- Only legacy rows are converted. Replaying this migration must not erase votes
-- or protection state produced after a completed conversion.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS group_ids BIGINT[] NOT NULL DEFAULT '{}',
    ADD COLUMN IF NOT EXISTS migration_note TEXT NOT NULL DEFAULT '';

ALTER TABLE scheduled_test_plans
    DROP CONSTRAINT IF EXISTS scheduled_test_plans_at_least_one_target,
    DROP CONSTRAINT IF EXISTS scheduled_test_plans_exactly_one_target,
    DROP CONSTRAINT IF EXISTS scheduled_test_plans_target_mode_check;

CREATE TEMP TABLE scheduled_test_policy_conversion (
    plan_id BIGINT PRIMARY KEY,
    scope_ids BIGINT[] NOT NULL,
    account_ids BIGINT[] NOT NULL,
    routing BOOLEAN NOT NULL,
    enabled BOOLEAN NOT NULL
) ON COMMIT DROP;

DO $$
DECLARE
    p RECORD;
    workflow JSONB;
    rule JSONB;
    action JSONB;
    next_rules JSONB;
    next_protection JSONB;
    selected_ids BIGINT[];
    scope_ids BIGINT[];
    account_ids BIGINT[];
    group_value JSONB;
    group_platform TEXT;
    issue TEXT;
    routing BOOLEAN;
    live_count INTEGER;
    platform_count INTEGER;
    policy_enabled BOOLEAN;
    expanded_scope BOOLEAN;
    action_group_id BIGINT;
BEGIN
    FOR p IN SELECT * FROM scheduled_test_plans
        WHERE cardinality(group_ids)=0 AND migration_note=''
        ORDER BY id FOR UPDATE
    LOOP
        workflow := p.protection->'group_workflow';
        selected_ids := '{}';
        scope_ids := '{}';
        issue := '';
        routing := FALSE;
        expanded_scope := FALSE;
        policy_enabled := COALESCE(p.protection->>'enabled','false')='true';
        next_rules := '[]'::jsonb;
        next_protection := COALESCE(p.protection,'{}'::jsonb)-'group_workflow';

        BEGIN
            IF jsonb_typeof(workflow)='object' THEN
                selected_ids := ARRAY[(workflow->>'pass_group_id')::bigint,(workflow->>'fail_group_id')::bigint];
            ELSIF p.group_id IS NOT NULL THEN
                selected_ids := ARRAY[p.group_id];
            ELSIF p.account_id IS NOT NULL THEN
                SELECT COALESCE(array_agg(DISTINCT ag.group_id ORDER BY ag.group_id),'{}'::bigint[])
                INTO selected_ids FROM account_groups ag WHERE ag.account_id=p.account_id;
            END IF;
            SELECT COALESCE(array_agg(id ORDER BY position),'{}'::bigint[])
            INTO selected_ids FROM (
                SELECT id,min(position) AS position FROM unnest(selected_ids) WITH ORDINALITY ids(id,position)
                WHERE id IS NOT NULL AND id>0 GROUP BY id
            ) deduplicated;
            scope_ids := selected_ids;
            IF cardinality(selected_ids)=0 THEN
                issue := '旧检测目标没有可用分组，请选择检测分组后重新启用。';
            ELSIF p.account_id IS NOT NULL OR p.target_mode<>'all_accounts' THEN
                issue := '旧指定账号或分组汇总检测已改为分组内所有账号，请确认检测范围后重新启用。';
            END IF;

            FOR rule IN SELECT value FROM jsonb_array_elements(COALESCE(p.protection->'rules','[]'::jsonb))
            LOOP
                IF rule->'on_pass' IS NULL OR rule->'on_pass'='null'::jsonb THEN
                    rule := rule || jsonb_build_object('on_pass',jsonb_build_object('scheduling','resume','group_mode','keep'));
                END IF;
                IF rule->'on_fail' IS NULL OR rule->'on_fail'='null'::jsonb THEN
                    rule := rule || jsonb_build_object('on_fail',jsonb_build_object('scheduling','pause','group_mode','keep'));
                END IF;
                rule := rule || jsonb_build_object('priority',COALESCE(rule->'priority','0'::jsonb),
                    'required_pass',COALESCE(rule->'required_pass','false'::jsonb));
                IF jsonb_typeof(workflow)='object' THEN
                    rule := rule || jsonb_build_object('priority',CASE
                        WHEN rule->>'test_definition_id'=workflow->>'review_test_id' THEN 100 ELSE 0 END,
                        'required_pass',FALSE);
                END IF;
                next_rules := next_rules || jsonb_build_array(rule);
                FOREACH action IN ARRAY ARRAY[rule->'on_pass',rule->'on_fail']
                LOOP
                    IF action->>'group_mode'='assign' THEN
                        routing := TRUE;
                        IF COALESCE(jsonb_array_length(action->'group_ids'),0)=0 THEN
                            issue := concat_ws(' ',NULLIF(issue,''),'旧分组动作没有目标分组，请选择目标分组后重新启用。');
                        END IF;
                        FOR group_value IN SELECT value FROM jsonb_array_elements(action->'group_ids')
                        LOOP
                            action_group_id := (group_value#>>'{}')::bigint;
                            scope_ids := array_append(scope_ids,action_group_id);
                            IF NOT action_group_id=ANY(selected_ids) THEN
                                selected_ids := array_append(selected_ids,action_group_id);
                                expanded_scope := TRUE;
                            END IF;
                        END LOOP;
                    END IF;
                END LOOP;
            END LOOP;
            next_protection := jsonb_set(next_protection,'{rules}',next_rules,TRUE);
            IF expanded_scope THEN
                issue := concat_ws(' ',NULLIF(issue,''),'动作目标分组已加入检测范围，请确认新增分组内的账号后重新启用。');
            END IF;
            IF jsonb_typeof(workflow)='object' AND
                (cardinality(selected_ids)<>2 OR jsonb_array_length(next_rules)<>2) THEN
                issue := concat_ws(' ',NULLIF(issue,''),'旧分组工作流配置不完整，请检查检测项目和分组后重新启用。');
            END IF;

            SELECT COALESCE(array_agg(DISTINCT id ORDER BY id),'{}'::bigint[])
            INTO scope_ids FROM unnest(scope_ids) id WHERE id IS NOT NULL;
            SELECT count(*),count(DISTINCT platform),min(platform)
            INTO live_count,platform_count,group_platform FROM groups
            WHERE id=ANY(scope_ids) AND deleted_at IS NULL AND status='active' AND platform<>'composite';
            IF live_count<>cardinality(scope_ids) OR platform_count<>1 THEN
                issue := concat_ws(' ',NULLIF(issue,''),'检测或动作分组已失效、不可用或属于不同平台，请调整后重新启用。');
            ELSIF p.account_id IS NOT NULL AND NOT EXISTS (
                SELECT 1 FROM accounts WHERE id=p.account_id AND deleted_at IS NULL AND platform=group_platform
            ) THEN
                issue := concat_ws(' ',NULLIF(issue,''),'旧指定账号已失效或与分组平台不一致，请调整后重新启用。');
            END IF;
        EXCEPTION WHEN invalid_text_representation OR numeric_value_out_of_range OR invalid_parameter_value THEN
            issue := '旧检测配置无法自动转换，请检查检测分组和判断动作后重新启用。';
            -- Keep the original rules as editable data, but retire the special
            -- workflow object even when a malformed legacy value is present.
            next_protection := COALESCE(p.protection,'{}'::jsonb)-'group_workflow';
            selected_ids := CASE WHEN p.group_id IS NULL THEN '{}'::bigint[] ELSE ARRAY[p.group_id] END;
            scope_ids := selected_ids;
        END;

        -- Workflow targets were JSON values without foreign keys. A deleted
        -- destination must disable only its plan, never abort the whole upgrade
        -- when it becomes the new group_id anchor. Keep the action JSON intact
        -- so administrators can see and repair the invalid destination.
        SELECT COALESCE(array_agg(selected.id ORDER BY selected.position),'{}'::bigint[])
        INTO selected_ids FROM unnest(selected_ids) WITH ORDINALITY selected(id,position)
        JOIN groups g ON g.id=selected.id;

        SELECT COALESCE(array_agg(DISTINCT account_id ORDER BY account_id),'{}'::bigint[])
        INTO account_ids FROM (
            SELECT ag.account_id FROM account_groups ag JOIN accounts a ON a.id=ag.account_id
                WHERE ag.group_id=ANY(scope_ids) AND a.deleted_at IS NULL
            UNION SELECT ma.account_id FROM scheduled_test_managed_accounts ma WHERE ma.plan_id=p.id
        ) participants;
        UPDATE scheduled_test_plans SET group_ids=selected_ids,group_id=selected_ids[1],account_id=NULL,
            target_mode='all_accounts',protection=next_protection,
            enabled=p.enabled AND issue='',migration_note=issue,latest_run_id='',updated_at=NOW()
        WHERE id=p.id;
        INSERT INTO scheduled_test_policy_conversion VALUES
            (p.id,scope_ids,account_ids,routing AND policy_enabled,p.enabled AND issue='');
    END LOOP;
END $$;

-- Old independent routing rules can overlap after a move. Disable every side
-- of a conflict rather than silently selecting a winner and changing routing.
WITH conflicts AS (
    SELECT left_plan.plan_id AS id,right_plan.plan_id AS other_id
    FROM scheduled_test_policy_conversion left_plan
    JOIN scheduled_test_policy_conversion right_plan ON left_plan.plan_id<>right_plan.plan_id
    WHERE left_plan.routing AND left_plan.enabled AND right_plan.routing AND right_plan.enabled
      AND (left_plan.scope_ids && right_plan.scope_ids OR left_plan.account_ids && right_plan.account_ids)
), reasons AS (
    SELECT id,string_agg('#'||other_id::text,'、' ORDER BY other_id) AS other_plans FROM conflicts GROUP BY id
)
UPDATE scheduled_test_plans p SET enabled=FALSE,
    migration_note='与检测计划 '||reasons.other_plans||' 的分组或账号范围重叠，已停用；请合并或调整范围后重新启用。'
FROM reasons WHERE p.id=reasons.id;

-- Results remain intact for history. Clear only the migrated policy state so
-- old votes, administrator decisions and retained targets cannot affect the
-- first generic-policy round.
CREATE TEMP TABLE scheduled_test_policy_reset_accounts ON COMMIT DROP AS
SELECT DISTINCT s.account_id FROM scheduled_test_protection_states s
JOIN scheduled_test_policy_conversion converted ON converted.plan_id=s.plan_id;
DELETE FROM scheduled_test_votes v USING scheduled_test_results r,scheduled_test_policy_conversion converted
WHERE v.result_id=r.id AND r.plan_id=converted.plan_id;
DELETE FROM scheduled_test_protection_states s USING scheduled_test_policy_conversion converted WHERE s.plan_id=converted.plan_id;
DELETE FROM scheduled_test_managed_accounts ma USING scheduled_test_policy_conversion converted WHERE ma.plan_id=converted.plan_id;

WITH released AS (
    UPDATE accounts a SET status='active',
        extra=COALESCE(a.extra,'{}'::jsonb)-'quality_protection_reason'-'quality_protection_trial',updated_at=NOW()
    WHERE a.id IN (SELECT account_id FROM scheduled_test_policy_reset_accounts)
      AND a.deleted_at IS NULL AND a.schedulable AND a.status IN ('active','quality_paused')
      AND (a.status='quality_paused' OR a.extra ? 'quality_protection_reason' OR a.extra ? 'quality_protection_trial')
      AND NOT EXISTS (SELECT 1 FROM scheduled_test_protection_states s WHERE s.account_id=a.id AND s.blocked)
    RETURNING a.id
)
INSERT INTO scheduler_outbox(event_type,account_id) SELECT 'account_changed',id FROM released;

ALTER TABLE scheduled_test_plans ADD CONSTRAINT scheduled_test_plans_target_mode_check CHECK (
    target_mode='all_accounts' AND account_id IS NULL AND (
        (cardinality(group_ids)>0 AND group_id IS NOT NULL AND group_id=group_ids[1])
        OR (NOT enabled AND cardinality(group_ids)=0 AND group_id IS NULL)
    )
);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_plans_group_ids ON scheduled_test_plans USING GIN(group_ids);
