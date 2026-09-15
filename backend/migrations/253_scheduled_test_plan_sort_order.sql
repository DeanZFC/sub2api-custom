-- Allow each configured scheduled-test rule to control the position of its
-- group's results on the user-facing test-results page. Group ordering is a
-- property of the rule, not of the group itself.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;

-- Keep existing installations deterministic while allowing new rules to use
-- zero as a valid first position after the migration.
UPDATE scheduled_test_plans
SET sort_order = id
WHERE sort_order = 0;

CREATE INDEX IF NOT EXISTS idx_stp_sort_order
    ON scheduled_test_plans(sort_order, id);
