ALTER TABLE scheduled_test_plans
    ADD COLUMN IF NOT EXISTS protection JSONB NOT NULL DEFAULT '{}';

CREATE TABLE IF NOT EXISTS scheduled_test_protection_states (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    test_definition_id BIGINT NOT NULL REFERENCES scheduled_test_definitions(id) ON DELETE RESTRICT,
    result_id BIGINT REFERENCES scheduled_test_results(id) ON DELETE SET NULL,
    result_started_at TIMESTAMPTZ NOT NULL,
    generation BIGINT NOT NULL DEFAULT 1,
    automated_verdict TEXT NOT NULL DEFAULT 'pending' CHECK (automated_verdict IN ('pending', 'pass', 'fail')),
    verdict TEXT NOT NULL DEFAULT 'pending' CHECK (verdict IN ('pending', 'pass', 'fail')),
    reason TEXT NOT NULL DEFAULT '',
    blocked BOOLEAN NOT NULL DEFAULT FALSE,
    completed BOOLEAN NOT NULL DEFAULT FALSE,
    rule_config JSONB NOT NULL DEFAULT '{}',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (plan_id, account_id, test_definition_id)
);
CREATE INDEX IF NOT EXISTS idx_st_protection_result ON scheduled_test_protection_states(result_id);
CREATE INDEX IF NOT EXISTS idx_st_protection_account_blocked ON scheduled_test_protection_states(account_id) WHERE blocked;

CREATE TABLE IF NOT EXISTS scheduled_test_votes (
    result_id BIGINT NOT NULL REFERENCES scheduled_test_results(id) ON DELETE CASCADE,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    generation BIGINT NOT NULL,
    vote TEXT NOT NULL CHECK (vote IN ('pass', 'fail')),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (result_id, user_id)
);
