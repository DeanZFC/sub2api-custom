-- Keep the final scoring configuration for inspection before retiring its
-- dedicated storage. Existing archives and shared traffic evidence stay intact.
DO $$
BEGIN
    IF to_regclass('channel_quality_score_config') IS NOT NULL THEN
        INSERT INTO quality_management_config_archive(kind, source_id, config)
        SELECT 'retired_score', id,
               jsonb_build_object('config', config, 'version', version,
                                  'collect_since', collect_since, 'updated_at', updated_at)
        FROM channel_quality_score_config
        ON CONFLICT (kind, source_id) DO NOTHING;
    END IF;
END;
$$;

DROP TABLE IF EXISTS channel_quality_score_config;
