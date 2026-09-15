-- Scheduled test plans may select a group and optionally narrow execution to
-- one account in that group. Replace the original XOR constraint, which
-- rejected the valid group+account combination used by the admin UI.
ALTER TABLE scheduled_test_plans
    DROP CONSTRAINT IF EXISTS scheduled_test_plans_exactly_one_target;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1
        FROM pg_constraint
        WHERE conrelid = 'scheduled_test_plans'::regclass
          AND conname = 'scheduled_test_plans_at_least_one_target'
    ) THEN
        ALTER TABLE scheduled_test_plans
            ADD CONSTRAINT scheduled_test_plans_at_least_one_target
            CHECK (account_id IS NOT NULL OR group_id IS NOT NULL);
    END IF;
END $$;
