-- Date reports cover all balance sources and redeemed non-balance components.
CREATE INDEX IF NOT EXISTS qq_bot_rewards_app_time
    ON qq_bot_rewards(app_id, created_at DESC, id DESC) WHERE amount > 0;
CREATE INDEX IF NOT EXISTS qq_bot_reward_codes_app_redeemed_time
    ON qq_bot_reward_codes(app_id, redeemed_at DESC, id DESC)
    WHERE reward_type <> 'balance' AND status IN ('pending','fulfilled');
-- Join only the date-filtered lottery ledger to its snapshot without parsing
-- historical reward keys or scanning every draw belonging to the application.
CREATE INDEX IF NOT EXISTS qq_bot_lottery_draws_app_reward_key
    ON qq_bot_lottery_draws(app_id, ('lottery:' || id::text));
