-- Persist the administrator's scheduled-test target selection.
-- group: execute through the selected group while hiding account IDs from users.
-- all_accounts: execute each schedulable account and expose its account ID.
-- account: execute only the selected account and expose its account ID.
ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS target_mode VARCHAR(20) NOT NULL DEFAULT 'all_accounts';

-- Preserve the behavior of existing plans created before target modes existed.
UPDATE scheduled_test_plans
SET target_mode = CASE
    WHEN account_id IS NOT NULL THEN 'account'
    ELSE 'all_accounts'
END
WHERE target_mode IS NULL OR target_mode = '' OR target_mode = 'all_accounts';

ALTER TABLE scheduled_test_plans
    DROP CONSTRAINT IF EXISTS scheduled_test_plans_target_mode_check;
ALTER TABLE scheduled_test_plans
    ADD CONSTRAINT scheduled_test_plans_target_mode_check
    CHECK (
        (target_mode = 'account' AND account_id IS NOT NULL)
        OR (target_mode IN ('group', 'all_accounts') AND group_id IS NOT NULL AND account_id IS NULL)
    );
CREATE INDEX IF NOT EXISTS idx_stp_target_mode ON scheduled_test_plans(target_mode);
