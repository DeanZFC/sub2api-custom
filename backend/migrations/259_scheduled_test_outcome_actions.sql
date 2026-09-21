-- Keep the last conclusive routing decision while a new test/voting round is pending.
ALTER TABLE scheduled_test_protection_states
    ADD COLUMN IF NOT EXISTS routing_verdict TEXT NOT NULL DEFAULT 'pending'
    CHECK (routing_verdict IN ('pending', 'pass', 'fail')),
    ADD COLUMN IF NOT EXISTS round_started_at TIMESTAMPTZ NOT NULL DEFAULT 'epoch';

-- Accounts moved out of a rule's source group must remain eligible for retests.
-- This is created only by an actual automatic group change, not by ordinary tests.
CREATE TABLE IF NOT EXISTS scheduled_test_managed_accounts (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    source_group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, account_id)
);
