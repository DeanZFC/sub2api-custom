-- Freeze reward eligibility before submitting a recharge. Never store account Sessions.
CREATE TABLE IF NOT EXISTS gpt_direct_redemptions (
    id BIGSERIAL PRIMARY KEY,
    scope TEXT NOT NULL,
    service_url TEXT NOT NULL,
    tenant_slug TEXT NOT NULL,
    user_id BIGINT NOT NULL REFERENCES users(id),
    request_key TEXT NOT NULL,
    code_hash TEXT NOT NULL,
    chances INTEGER NOT NULL CHECK (chances BETWEEN 0 AND 1000),
    task_id TEXT,
    task_token_cipher TEXT NOT NULL DEFAULT '',
    token_hash TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'awaiting_receipt',
    poll_after TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (scope, request_key),
    UNIQUE (scope, task_id)
);
CREATE INDEX IF NOT EXISTS gpt_direct_redemptions_poll ON gpt_direct_redemptions(poll_after)
    WHERE chances > 0 AND task_token_cipher <> '';

-- A CDK grants chances once across requests, users, refreshes and worker instances.
CREATE TABLE IF NOT EXISTS qq_bot_lottery_chance_grants (
    source_key TEXT PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    chances INTEGER NOT NULL CHECK (chances BETWEEN 1 AND 1000),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS qq_bot_lottery_chance_grants_user ON qq_bot_lottery_chance_grants(user_id);
