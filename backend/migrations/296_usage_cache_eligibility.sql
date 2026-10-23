-- Nullable until rebuilt: legacy totals cannot distinguish synchronous input.
-- Preserve historical usage/costs even when raw logs have already expired.
ALTER TABLE usage_dashboard_hourly
    ADD COLUMN IF NOT EXISTS cache_rate_numerator BIGINT,
    ADD COLUMN IF NOT EXISTS cache_rate_denominator BIGINT;

ALTER TABLE usage_dashboard_daily
    ADD COLUMN IF NOT EXISTS cache_rate_numerator BIGINT,
    ADD COLUMN IF NOT EXISTS cache_rate_denominator BIGINT;

-- Existing counters remain unknown until normal aggregation or an explicit
-- recomputation fills them. Do not reset the watermark: raw logs may have
-- expired, so replaying history could overwrite complete usage/cost totals.
