-- Preserve numerator/denominator totals so group/model/time aggregation can
-- weight output speed by actual generation time without averaging rates.
ALTER TABLE channel_monitor_v2_metrics_1m
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
ALTER TABLE channel_monitor_v2_user_metrics_1m
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
ALTER TABLE channel_monitor_v2_metrics_rollup
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;
ALTER TABLE channel_monitor_v2_user_metrics_rollup
    ADD COLUMN IF NOT EXISTS tps_output_tokens BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_generation_ms BIGINT NOT NULL DEFAULT 0,
    ADD COLUMN IF NOT EXISTS tps_samples BIGINT NOT NULL DEFAULT 0;

-- Preserve existing coverage and historical aggregates. Raw logs may already
-- have expired, so restarting the shared backfill would erase valid historical
-- counts, errors and cache totals while trying to reconstruct TPS.
-- Legacy TPS stays unknown (zero eligible samples); ordinary recent-window
-- refreshes populate the new fields without forcing a historical rebuild.
