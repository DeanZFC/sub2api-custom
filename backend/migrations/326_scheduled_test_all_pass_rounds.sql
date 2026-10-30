-- Whole-round evidence is independent of individual result retention. Each
-- plan/account/public model/run contributes at most one consecutive success.
CREATE TABLE IF NOT EXISTS scheduled_test_all_pass_rounds (
    id BIGSERIAL PRIMARY KEY,
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_key TEXT NOT NULL,
    run_id TEXT NOT NULL CHECK (run_id <> ''),
    started_at TIMESTAMPTZ NOT NULL,
    rule_version TEXT NOT NULL,
    failed_definition_ids BIGINT[] NOT NULL DEFAULT '{}',
    verdict TEXT NOT NULL DEFAULT 'pending' CHECK (verdict IN ('pending', 'pass', 'fail')),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (plan_id, account_id, model_key, run_id)
);
CREATE INDEX IF NOT EXISTS idx_st_all_pass_round_history
    ON scheduled_test_all_pass_rounds(plan_id, account_id, model_key, started_at DESC, id DESC);
