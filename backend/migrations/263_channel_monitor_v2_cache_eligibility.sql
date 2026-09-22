-- Keep synchronous traffic in request/token metrics without treating its
-- missing provider cache telemetry as a cache miss.

ALTER TABLE IF EXISTS channel_monitor_v2_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE IF EXISTS channel_monitor_v2_user_metrics_1m
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE IF EXISTS channel_monitor_v2_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0;

ALTER TABLE IF EXISTS channel_monitor_v2_user_metrics_rollup
    ADD COLUMN IF NOT EXISTS cache_eligible_input_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_creation_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS cache_eligible_read_tokens BIGINT NOT NULL DEFAULT 0;

-- Existing aggregate rows cannot distinguish synchronous input from cache
-- eligible input. Recompute the retained window with the new classification.
UPDATE channel_monitor_v2_watermarks
SET usage_coverage_start = NULL,
    error_coverage_start = NULL,
    data_through = NULL,
    last_successful_at = NULL,
    backfill_cursor = NULL,
    updated_at = NOW()
WHERE id = 1;
