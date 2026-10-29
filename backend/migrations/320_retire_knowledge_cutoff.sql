-- Migration 319 has already been released and must keep its original checksum.
-- Remove the standalone question from active plans without changing their
-- other selected tests. Preserve historical results and quality audit events.
UPDATE scheduled_test_definitions
SET enabled = FALSE, updated_at = NOW()
WHERE key = 'knowledge_cutoff' AND enabled;

DROP TABLE IF EXISTS pg_temp.retired_knowledge_cutoff_combination_states;
DROP TABLE IF EXISTS pg_temp.retired_knowledge_cutoff_combinations;

CREATE OR REPLACE FUNCTION scheduled_test_retirement_condition_references_definition(condition JSONB, target_id BIGINT)
RETURNS BOOLEAN LANGUAGE plpgsql IMMUTABLE AS $$
DECLARE child JSONB;
BEGIN
    IF jsonb_typeof(condition) = 'object' THEN
        IF condition->>'test_definition_id' = target_id::text THEN
            RETURN TRUE;
        END IF;
        FOR child IN SELECT value FROM jsonb_each(condition) LOOP
            IF scheduled_test_retirement_condition_references_definition(child, target_id) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    ELSIF jsonb_typeof(condition) = 'array' THEN
        FOR child IN SELECT value FROM jsonb_array_elements(condition) LOOP
            IF scheduled_test_retirement_condition_references_definition(child, target_id) THEN
                RETURN TRUE;
            END IF;
        END LOOP;
    END IF;
    RETURN FALSE;
END;
$$;

CREATE TEMP TABLE retired_knowledge_cutoff_combinations ON COMMIT DROP AS
SELECT p.id AS plan_id, combination.ordinality AS combination_ordinal,
       combination.value->>'id' AS combination_id
FROM scheduled_test_plans p
CROSS JOIN scheduled_test_definitions d
CROSS JOIN LATERAL jsonb_array_elements(
    CASE WHEN jsonb_typeof(p.protection->'combinations') = 'array'
         THEN p.protection->'combinations' ELSE '[]'::jsonb END
) WITH ORDINALITY AS combination(value, ordinality)
WHERE d.key = 'knowledge_cutoff'
  AND scheduled_test_retirement_condition_references_definition(combination.value->'condition', d.id);

CREATE TEMP TABLE retired_knowledge_cutoff_combination_states ON COMMIT DROP AS
SELECT s.plan_id, s.account_id, s.model_key,
       COALESCE((SELECT jsonb_agg(rule_id ORDER BY ordinality)
                 FROM jsonb_array_elements(s.rule_ids) WITH ORDINALITY AS rule(rule_id, ordinality)
                 WHERE NOT EXISTS (SELECT 1 FROM retired_knowledge_cutoff_combinations retired
                                   WHERE retired.plan_id = s.plan_id
                                     AND retired.combination_id = rule.rule_id#>>'{}')), '[]'::jsonb) AS remaining_rule_ids
FROM scheduled_test_combination_states s
WHERE EXISTS (SELECT 1 FROM jsonb_array_elements_text(s.rule_ids) rule_id
              JOIN retired_knowledge_cutoff_combinations retired
                ON retired.plan_id = s.plan_id AND retired.combination_id = rule_id);

WITH retired AS (
    SELECT id FROM scheduled_test_definitions WHERE key = 'knowledge_cutoff'
), affected AS (
    SELECT p.id, retired.id AS retired_id,
           ARRAY(SELECT selected FROM unnest(p.test_definition_ids) AS selected
                 WHERE selected <> retired.id) AS remaining_ids,
           COALESCE((SELECT jsonb_agg(rule ORDER BY ord)
                     FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules') = 'array'
                                                    THEN p.protection->'rules' ELSE '[]'::jsonb END)
                          WITH ORDINALITY AS entry(rule, ord)
                     WHERE rule->>'test_definition_id' IS DISTINCT FROM retired.id::text), '[]'::jsonb) AS remaining_rules,
           COALESCE((SELECT jsonb_agg(combination.value ORDER BY combination.ordinality)
                     FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'combinations') = 'array'
                                                    THEN p.protection->'combinations' ELSE '[]'::jsonb END)
                          WITH ORDINALITY AS combination(value, ordinality)
                     WHERE NOT EXISTS (SELECT 1 FROM retired_knowledge_cutoff_combinations removed
                                       WHERE removed.plan_id = p.id
                                         AND removed.combination_ordinal = combination.ordinality)), '[]'::jsonb) AS remaining_combinations
    FROM scheduled_test_plans p CROSS JOIN retired
    WHERE p.test_definition_id = retired.id OR retired.id = ANY(p.test_definition_ids)
       OR EXISTS (SELECT 1 FROM jsonb_array_elements(CASE WHEN jsonb_typeof(p.protection->'rules') = 'array'
                                                          THEN p.protection->'rules' ELSE '[]'::jsonb END) rule
                  WHERE rule->>'test_definition_id' = retired.id::text)
       OR EXISTS (SELECT 1 FROM retired_knowledge_cutoff_combinations removed WHERE removed.plan_id = p.id)
)
UPDATE scheduled_test_plans p
SET test_definition_ids = affected.remaining_ids,
    test_definition_id = CASE WHEN p.test_definition_id = affected.retired_id
                              THEN affected.remaining_ids[1] ELSE p.test_definition_id END,
    enabled = p.enabled AND cardinality(affected.remaining_ids) > 0,
    protection = CASE WHEN affected.remaining_rules = '[]'::jsonb
                           OR (p.protection->>'mode' = 'combined' AND affected.remaining_combinations = '[]'::jsonb)
                      THEN jsonb_set(jsonb_set(jsonb_set(p.protection, '{rules}', affected.remaining_rules, true),
                                               '{combinations}', affected.remaining_combinations, true),
                                     '{enabled}', 'false'::jsonb, true)
                      ELSE jsonb_set(jsonb_set(p.protection, '{rules}', affected.remaining_rules, true),
                                     '{combinations}', affected.remaining_combinations, true) END,
    updated_at = NOW()
FROM affected WHERE p.id = affected.id;

UPDATE scheduled_test_combination_states s
SET rule_ids = adjusted.remaining_rule_ids
FROM retired_knowledge_cutoff_combination_states adjusted
WHERE s.plan_id = adjusted.plan_id AND s.account_id = adjusted.account_id AND s.model_key = adjusted.model_key
  AND adjusted.remaining_rule_ids <> '[]'::jsonb;
DELETE FROM scheduled_test_combination_states s USING retired_knowledge_cutoff_combination_states adjusted
WHERE s.plan_id = adjusted.plan_id AND s.account_id = adjusted.account_id AND s.model_key = adjusted.model_key
  AND adjusted.remaining_rule_ids = '[]'::jsonb;

-- Delete only obsolete holds. Existing delete triggers recompute account and
-- model projections from unaffected rules. A combined policy remains enabled
-- only when it has at least one independent surviving condition.
DELETE FROM scheduled_test_protection_states s USING scheduled_test_definitions d
WHERE d.key = 'knowledge_cutoff' AND s.test_definition_id = d.id;
DELETE FROM scheduled_test_pass_rate_states s USING scheduled_test_definitions d
WHERE d.key = 'knowledge_cutoff' AND s.test_definition_id = d.id;

-- On a fresh installation, 319 is immediately undone and no definition
-- remains. Upgraded installations retain a disabled row only if referenced.
DELETE FROM scheduled_test_definitions d
WHERE d.key = 'knowledge_cutoff'
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_plans p
                  WHERE p.test_definition_id = d.id OR d.id = ANY(p.test_definition_ids))
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_plan_definitions pd WHERE pd.test_definition_id = d.id)
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_results r WHERE r.test_definition_id = d.id)
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_protection_states s WHERE s.test_definition_id = d.id)
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_pass_rate_states s WHERE s.test_definition_id = d.id)
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_pass_rate_evidence e WHERE e.test_definition_id = d.id)
  AND NOT EXISTS (SELECT 1 FROM scheduled_test_protection_events e WHERE e.test_definition_id = d.id);

DROP FUNCTION scheduled_test_retirement_condition_references_definition(JSONB, BIGINT);
