-- NULL retains the existing cron schedule; periods are opt-in per strategy.
ALTER TABLE scheduled_test_plans ADD COLUMN IF NOT EXISTS schedule_config JSONB;
COMMENT ON COLUMN scheduled_test_plans.schedule_config IS
    'Optional daily time periods with an explicit timezone and per-period interval_minutes';
