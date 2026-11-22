-- Keep recharge-backed chances live while managing the rule in the task list.
-- Only configuration moves. Draws, grants and task progress are untouched.
WITH existing AS (
    SELECT value::jsonb AS config
    FROM settings
    WHERE key = 'qq_bot_checkin_config'
      AND value::jsonb #> '{growth,lottery_recharge}' IS NOT NULL
), growth AS (
    SELECT config, config -> 'growth' AS rules,
           config #> '{growth,lottery_recharge}' AS recharge,
           COALESCE(NULLIF(config #> '{growth,periodic_tasks}', 'null'::jsonb), '[]'::jsonb) AS periodic,
           COALESCE(NULLIF(config #> '{growth,tasks}', 'null'::jsonb), '[]'::jsonb) AS tasks
    FROM existing
), converted AS (
    SELECT growth.config, (rules - 'lottery_recharge') ||
        CASE WHEN jsonb_typeof(recharge) = 'object'
              AND NOT EXISTS (
                  SELECT 1 FROM jsonb_array_elements(periodic) AS task
                  WHERE task -> 'live_lottery_recharge' = 'true'::jsonb
              )
            THEN jsonb_build_object('periodic_tasks', jsonb_build_array(jsonb_build_object(
                'id', task_id.id,
                'name', '累计充值赠抽奖次数',
                'kind', 'recharge_total',
                'target', recharge -> 'recharge_step',
                'reward', jsonb_build_object('type', 'chances', 'quantity', recharge -> 'chances_per_step'),
                'chances', recharge -> 'chances_per_step',
                'enabled', true,
                'live_lottery_recharge', true
            )) || periodic)
            ELSE '{}'::jsonb
        END AS rules
    FROM growth
    CROSS JOIN LATERAL (
        SELECT candidate.id
        FROM generate_series(0, jsonb_array_length(tasks) + jsonb_array_length(periodic)) AS seq(suffix)
        CROSS JOIN LATERAL (
            SELECT CASE WHEN seq.suffix = 0 THEN 'lottery_recharge'
                        ELSE 'lottery_recharge_' || seq.suffix::text END AS id
        ) AS candidate
        WHERE NOT EXISTS (
            SELECT 1 FROM jsonb_array_elements(tasks || periodic) AS task
            WHERE task ->> 'id' = candidate.id
        )
        ORDER BY seq.suffix
        LIMIT 1
    ) AS task_id
)
UPDATE settings
SET value = jsonb_set(converted.config, '{growth}', converted.rules)::text,
    updated_at = NOW()
FROM converted
WHERE settings.key = 'qq_bot_checkin_config';
