-- Move the existing recharge rule without replaying recharge or draw history.
-- Keep an explicitly disabled or already edited growth rule on retry.
INSERT INTO settings AS checkin (key, value, updated_at)
SELECT 'qq_bot_checkin_config',
       jsonb_build_object('growth', jsonb_build_object('lottery_recharge', jsonb_build_object(
           'recharge_step', COALESCE(NULLIF(lottery.value::jsonb -> 'recharge_step', 'null'::jsonb), '10'::jsonb),
           'chances_per_step', COALESCE(NULLIF(lottery.value::jsonb -> 'chances_per_step', 'null'::jsonb), '1'::jsonb)
       )))::text,
       NOW()
FROM settings AS lottery
WHERE lottery.key = 'qq_bot_lottery_config'
ON CONFLICT (key) DO UPDATE
SET value = jsonb_set(
        CASE WHEN jsonb_typeof(checkin.value::jsonb) = 'object' THEN checkin.value::jsonb ELSE '{}'::jsonb END,
        '{growth}',
        (CASE WHEN jsonb_typeof(checkin.value::jsonb -> 'growth') = 'object'
              THEN checkin.value::jsonb -> 'growth' ELSE '{}'::jsonb END)
            || (EXCLUDED.value::jsonb -> 'growth')
    )::text,
    updated_at = NOW()
WHERE checkin.value::jsonb #> '{growth,lottery_recharge}' IS NULL;
