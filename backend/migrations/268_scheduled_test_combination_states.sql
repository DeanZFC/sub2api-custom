-- Combination actions own one scheduling hold per strategy and account.
-- It survives pending rounds and result deletion; only a matching resume
-- action or a strategy reset releases it. Existing per-test holds remain
-- independent, including their bounded cache recovery trials.
CREATE TABLE IF NOT EXISTS scheduled_test_combination_states (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    blocked BOOLEAN NOT NULL DEFAULT FALSE,
    reason TEXT NOT NULL DEFAULT '',
    rule_ids JSONB NOT NULL DEFAULT '[]'::jsonb,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, account_id)
);

CREATE INDEX IF NOT EXISTS idx_st_combination_account_blocked
    ON scheduled_test_combination_states(account_id) WHERE blocked;
