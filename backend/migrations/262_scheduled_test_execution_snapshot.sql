ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS latest_run_id TEXT NOT NULL DEFAULT '';

ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS run_id TEXT NOT NULL DEFAULT '',
    ADD COLUMN IF NOT EXISTS protection_decision JSONB;

CREATE INDEX IF NOT EXISTS idx_scheduled_test_results_run
    ON scheduled_test_results (plan_id, run_id, started_at DESC, id DESC);
