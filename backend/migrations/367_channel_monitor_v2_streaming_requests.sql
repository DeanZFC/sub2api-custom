-- Old buckets mix synchronous and streaming traffic without a transport dimension.
-- Invalidate the derived facts and restart the normal bounded backfill from raw logs.
DELETE FROM channel_monitor_v2_latency_histograms_1m;
DELETE FROM channel_monitor_v2_error_metrics_1m;
DELETE FROM channel_monitor_v2_user_metrics_1m;
DELETE FROM channel_monitor_v2_metrics_1m;
DELETE FROM channel_monitor_v2_metrics_rollup;
DELETE FROM channel_monitor_v2_user_metrics_rollup;
DELETE FROM channel_monitor_v2_latency_histograms_rollup;
DELETE FROM channel_monitor_v2_error_metrics_rollup;

UPDATE channel_monitor_v2_watermarks
SET usage_coverage_start = NULL, error_coverage_start = NULL, data_through = NULL,
    last_successful_at = NULL, backfill_cursor = NULL, updated_at = NOW()
WHERE id = 1;

-- This timestamp also came from the unfiltered activity query.
UPDATE group_probes SET last_user_at = NULL WHERE last_user_at IS NOT NULL;
