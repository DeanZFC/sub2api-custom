-- Exclusions belong to one strategy; they do not disable business scheduling.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS excluded_account_ids BIGINT[] NOT NULL DEFAULT '{}';
