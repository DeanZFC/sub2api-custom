-- Test request usage is separate from billable user usage logs.
ALTER TABLE scheduled_test_results ADD COLUMN IF NOT EXISTS execution_info JSONB;
CREATE TABLE IF NOT EXISTS scheduled_test_account_backoffs (
 plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 policy_key TEXT NOT NULL,
 consecutive_failures INTEGER NOT NULL DEFAULT 0,
 next_check_at TIMESTAMPTZ NOT NULL,
 observed_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(plan_id, account_id)
);
