-- A local session block used to create an upstream_forbidden model card. Only
-- correct a historical bucket when retained local audit records account for
-- its entire upstream_forbidden count. Replaying raw history could erase other
-- aggregates after the source logs have expired.
CREATE TEMP TABLE channel_monitor_block_counts ON COMMIT DROP AS
WITH local_requests AS (
    SELECT DISTINCT ON (COALESCE(NULLIF(e.request_id, ''), 'error:' || e.id::text))
           e.created_at, e.user_id, COALESCE(e.group_id, 0) AS group_id,
           lower(CASE WHEN g.platform = 'composite'
                      THEN COALESCE(NULLIF(NULLIF(lower(trim(e.platform)), ''), 'composite'), 'unknown')
                      ELSE COALESCE(NULLIF(trim(e.platform), ''), 'unknown') END) AS platform,
           COALESCE(NULLIF(trim(e.requested_model), ''), NULLIF(trim(e.model), ''), 'unknown') AS model
    FROM ops_error_logs e
    LEFT JOIN groups g ON g.id = e.group_id
    WHERE e.error_type = 'cyber_policy_session_blocked' AND e.error_source = 'gateway_local'
      AND e.account_id IS NULL AND e.status_code = 403 AND NOT e.is_count_tokens
      AND e.created_at >= now() - interval '90 days'
      AND e.created_at < (SELECT data_through FROM channel_monitor_v2_watermarks WHERE id = 1)
      AND NOT EXISTS (
          SELECT 1 FROM ops_error_logs other
          WHERE other.request_id = NULLIF(e.request_id, '') AND NOT other.is_count_tokens
            AND other.error_type IS DISTINCT FROM e.error_type
            AND (other.status_code >= 400 OR other.error_type = 'cyber_policy')
      )
    ORDER BY COALESCE(NULLIF(e.request_id, ''), 'error:' || e.id::text), e.created_at DESC, e.id DESC
)
SELECT seconds, date_bin(make_interval(secs => seconds), created_at, timestamptz 'epoch') AS bucket_start,
       platform, group_id, model, user_id, GROUPING(user_id) AS all_users, count(*) AS blocks, max(created_at) AS last_block
FROM local_requests CROSS JOIN (VALUES (60), (300), (3600), (43200), (86400)) AS tiers(seconds)
GROUP BY 1, 2, 3, 4, 5, GROUPING SETS ((), (user_id));

DO $$
DECLARE
    tier text;
    metrics text;
    errors text;
    target_table text;
    source_rows text;
    match_rows text;
    dimension_match text := '(m.bucket_start,m.platform,m.group_id,m.model) = (c.bucket_start,c.platform,c.group_id,c.model)';
    empty_usage text := 'm.success_requests = 0 AND m.input_tokens = 0 AND m.output_tokens = 0
        AND m.cache_creation_tokens = 0 AND m.cache_read_tokens = 0 AND m.ttft_count = 0
        AND m.duration_count = 0 AND m.tps_samples = 0';
    bucket_match text;
BEGIN
    IF NOT EXISTS (SELECT FROM channel_monitor_block_counts) THEN RETURN; END IF;
    -- Serialize with the normal worker while checking and removing these rows.
    LOCK TABLE channel_monitor_v2_error_metrics_1m, channel_monitor_v2_user_metrics_1m, channel_monitor_v2_metrics_1m,
               channel_monitor_v2_metrics_rollup, channel_monitor_v2_user_metrics_rollup, channel_monitor_v2_error_metrics_rollup
        IN SHARE ROW EXCLUSIVE MODE;
    FOREACH tier IN ARRAY ARRAY['1m', 'rollup'] LOOP
        metrics := 'channel_monitor_v2_metrics_' || tier;
        errors := 'channel_monitor_v2_error_metrics_' || tier;
        bucket_match := CASE WHEN tier = '1m' THEN 'c.seconds = 60' ELSE 'm.bucket_seconds = c.seconds' END;

        -- Protect known upstream attempts and buckets with additional 403s.
        EXECUTE format('CREATE TEMP TABLE confirmed_block_buckets ON COMMIT DROP AS
            SELECT c.* FROM channel_monitor_block_counts c JOIN %I m USING (bucket_start, platform, group_id, model)
            JOIN %I e USING (bucket_start, platform, group_id, model)
            WHERE c.all_users = 1 AND %s AND %s AND m.computed_at >= c.last_block
              AND m.error_requests - m.upstream_affected_requests >= c.blocks
              AND e.taxonomy_version = 1 AND e.error_category = ''upstream_forbidden'' AND e.error_requests = c.blocks',
            metrics, errors, bucket_match, CASE WHEN tier = '1m' THEN 'TRUE' ELSE 'e.bucket_seconds = c.seconds' END);

        EXECUTE format('DELETE FROM %I m USING confirmed_block_buckets c
            WHERE %s AND %s AND m.error_category = ''upstream_forbidden'' AND m.taxonomy_version = 1',
            errors, dimension_match, bucket_match);

        FOREACH target_table IN ARRAY ARRAY[metrics, 'channel_monitor_v2_user_metrics_' || tier] LOOP
            source_rows := 'confirmed_block_buckets c';
            match_rows := dimension_match || ' AND ' || bucket_match || ' AND m.computed_at >= c.last_block';
            IF target_table <> metrics THEN
                source_rows := 'channel_monitor_block_counts c JOIN confirmed_block_buckets b USING (seconds,bucket_start,platform,group_id,model)';
                match_rows := match_rows || ' AND c.all_users = 0 AND m.user_id = c.user_id';
            END IF;
            -- Remove empty cards, retaining throughput, successes, and other failures.
            EXECUTE format('DELETE FROM %I m USING %s WHERE %s AND m.error_requests = c.blocks AND %s',
                target_table, source_rows, match_rows, empty_usage);
            EXECUTE format('UPDATE %I m SET error_requests = m.error_requests - c.blocks FROM %s
                WHERE %s AND m.error_requests >= c.blocks', target_table, source_rows, match_rows);
        END LOOP;
        DROP TABLE confirmed_block_buckets;
    END LOOP;
END $$;
