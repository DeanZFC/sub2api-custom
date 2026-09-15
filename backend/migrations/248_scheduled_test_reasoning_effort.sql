-- Store the requested reasoning effort for configurable scheduled tests.
-- Empty preserves legacy plans and lets the upstream choose its default.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(20) NOT NULL DEFAULT '';
