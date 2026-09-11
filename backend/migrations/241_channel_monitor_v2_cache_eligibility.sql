-- Keep synchronous traffic in request/token metrics without treating its
-- non-existent cache telemetry as a cache miss.

ALTER TABLE IF EXISTS channel_monitor_v2_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE IF EXISTS channel_monitor_v2_user_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE IF EXISTS channel_monitor_v2_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE IF EXISTS channel_monitor_v2_user_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0;

-- Existing aggregate rows cannot tell synchronous input apart from cache
-- eligible input. Reset the monitor cursor so the background aggregator
-- recomputes the retained window with the new classification.
UPDATE channel_monitor_v2_watermarks
SET usage_coverage_start = NULL,
    error_coverage_start = NULL,
    data_through = NULL,
    last_successful_at = NULL,
    backfill_cursor = NULL,
    updated_at = NOW()
WHERE id = 1;
