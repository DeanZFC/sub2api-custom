ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS test_definition_ids BIGINT[] NOT NULL DEFAULT '{}';

UPDATE scheduled_test_plans
SET test_definition_ids = ARRAY[test_definition_id]
WHERE test_definition_id IS NOT NULL AND cardinality(test_definition_ids) = 0;

-- PostgreSQL cannot attach foreign keys to array elements. Maintain references
-- transactionally so a concurrent definition deletion cannot orphan a selection.
CREATE TABLE IF NOT EXISTS scheduled_test_plan_definitions (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    test_definition_id BIGINT NOT NULL REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT,
    PRIMARY KEY (plan_id, test_definition_id)
);
CREATE INDEX IF NOT EXISTS idx_stpd_definition ON scheduled_test_plan_definitions(test_definition_id);

CREATE OR REPLACE FUNCTION sync_scheduled_test_plan_definitions() RETURNS TRIGGER AS $$
BEGIN
    DELETE FROM scheduled_test_plan_definitions
    WHERE plan_id = NEW.id AND NOT (test_definition_id = ANY(NEW.test_definition_ids));
    INSERT INTO scheduled_test_plan_definitions (plan_id, test_definition_id)
    SELECT NEW.id, definition_id
    FROM unnest(NEW.test_definition_ids) AS selected(definition_id)
    ORDER BY definition_id
    ON CONFLICT DO NOTHING;
    RETURN NEW;
END;
$$ LANGUAGE plpgsql;

DROP TRIGGER IF EXISTS trg_stp_sync_definitions ON scheduled_test_plans;
CREATE TRIGGER trg_stp_sync_definitions
    AFTER INSERT OR UPDATE OF test_definition_ids ON scheduled_test_plans
    FOR EACH ROW EXECUTE FUNCTION sync_scheduled_test_plan_definitions();

INSERT INTO scheduled_test_plan_definitions (plan_id, test_definition_id)
SELECT p.id, definition_id
FROM scheduled_test_plans p
CROSS JOIN LATERAL unnest(p.test_definition_ids) AS selected(definition_id)
ON CONFLICT DO NOTHING;

ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS test_definition_id BIGINT REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT;
ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS target_mode VARCHAR(20) NOT NULL DEFAULT '';

-- Snapshot the old single-type association once. Future edits to a plan must
-- never relabel its previous executions or change account ID visibility.
UPDATE scheduled_test_results r
SET test_definition_id = p.test_definition_id, target_mode = p.target_mode
FROM scheduled_test_plans p
WHERE p.id = r.plan_id AND r.target_mode = '';

CREATE INDEX IF NOT EXISTS idx_str_target_definition_history
    ON scheduled_test_results (group_id, account_id, test_definition_id, started_at DESC, id DESC);
