-- Shared account pool: user-owned listings and independent settlement ledger.
ALTER TABLE accounts ADD COLUMN IF NOT EXISTS account_scope VARCHAR(20) NOT NULL DEFAULT 'system';
CREATE INDEX IF NOT EXISTS idx_accounts_scope_platform_status ON accounts(account_scope, platform, status, schedulable);

CREATE TABLE IF NOT EXISTS shared_account_listings (
    id BIGSERIAL PRIMARY KEY,
    owner_user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    account_id BIGINT NOT NULL UNIQUE REFERENCES accounts(id) ON DELETE CASCADE,
    platform VARCHAR(50) NOT NULL,
    display_name VARCHAR(100) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    proxy_config_encrypted TEXT,
    concurrency_limit INTEGER NOT NULL DEFAULT 1,
    concurrency_multiplier NUMERIC(10,4) NOT NULL DEFAULT 1,
    sell_rate NUMERIC(10,4) NOT NULL DEFAULT 1,
    fee_rate_override NUMERIC(7,4),
    total_call_count BIGINT NOT NULL DEFAULT 0,
    last_called_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ,
    CONSTRAINT shared_listing_status_ck CHECK (status IN ('testing','active','paused','invalid','suspended','deleted')),
    CONSTRAINT shared_listing_concurrency_ck CHECK (concurrency_limit > 0),
    CONSTRAINT shared_listing_rates_ck CHECK (concurrency_multiplier > 0 AND sell_rate >= 0),
    CONSTRAINT shared_listing_fee_ck CHECK (fee_rate_override IS NULL OR (fee_rate_override >= 0 AND fee_rate_override <= 100))
);
CREATE INDEX IF NOT EXISTS idx_shared_listing_owner_status ON shared_account_listings(owner_user_id, status);
CREATE INDEX IF NOT EXISTS idx_shared_listing_platform_status ON shared_account_listings(platform, status);

CREATE TABLE IF NOT EXISTS shared_account_usage_ledger (
    id BIGSERIAL PRIMARY KEY,
    request_id VARCHAR(255) NOT NULL UNIQUE,
    usage_log_id BIGINT UNIQUE,
    listing_id BIGINT NOT NULL REFERENCES shared_account_listings(id),
    owner_user_id BIGINT NOT NULL REFERENCES users(id),
    consumer_user_id BIGINT NOT NULL REFERENCES users(id),
    gross_cost NUMERIC(20,8) NOT NULL,
    fee_rate_percent NUMERIC(7,4) NOT NULL,
    platform_fee NUMERIC(20,8) NOT NULL,
    owner_amount NUMERIC(20,8) NOT NULL,
    action VARCHAR(20) NOT NULL DEFAULT 'earn',
    frozen_until TIMESTAMPTZ,
    released_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CONSTRAINT shared_usage_action_ck CHECK (action IN ('earn','reverse'))
);
CREATE INDEX IF NOT EXISTS idx_shared_usage_owner_created ON shared_account_usage_ledger(owner_user_id, created_at DESC);
CREATE INDEX IF NOT EXISTS idx_shared_usage_listing_created ON shared_account_usage_ledger(listing_id, created_at DESC);

CREATE TABLE IF NOT EXISTS shared_account_wallets (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL UNIQUE REFERENCES users(id) ON DELETE CASCADE,
    pending_amount NUMERIC(20,8) NOT NULL DEFAULT 0,
    available_amount NUMERIC(20,8) NOT NULL DEFAULT 0,
    frozen_amount NUMERIC(20,8) NOT NULL DEFAULT 0,
    total_earned NUMERIC(20,8) NOT NULL DEFAULT 0,
    total_transferred NUMERIC(20,8) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE TABLE IF NOT EXISTS shared_account_wallet_ledger (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    listing_id BIGINT REFERENCES shared_account_listings(id),
    usage_ledger_id BIGINT REFERENCES shared_account_usage_ledger(id),
    action VARCHAR(30) NOT NULL,
    amount NUMERIC(20,8) NOT NULL,
    idempotency_key VARCHAR(255) UNIQUE,
    available_after NUMERIC(20,8),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_shared_wallet_ledger_user_created ON shared_account_wallet_ledger(user_id, created_at DESC);

CREATE TABLE IF NOT EXISTS shared_account_call_stats (
    id BIGSERIAL PRIMARY KEY,
    listing_id BIGINT NOT NULL REFERENCES shared_account_listings(id) ON DELETE CASCADE,
    request_id VARCHAR(255) NOT NULL UNIQUE,
    model VARCHAR(100),
    result_status VARCHAR(20) NOT NULL,
    duration_ms BIGINT,
    charged_amount NUMERIC(20,8) NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_shared_call_stats_listing_created ON shared_account_call_stats(listing_id, created_at DESC);

CREATE TABLE IF NOT EXISTS shared_account_withdrawals (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id),
    amount NUMERIC(20,8) NOT NULL CHECK (amount > 0),
    status VARCHAR(20) NOT NULL DEFAULT 'completed',
    ledger_id BIGINT REFERENCES shared_account_wallet_ledger(id),
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    processed_at TIMESTAMPTZ,
    idempotency_key VARCHAR(128) NOT NULL,
    balance_after NUMERIC(20,8) NOT NULL,
    UNIQUE(user_id, idempotency_key)
);
CREATE INDEX IF NOT EXISTS idx_shared_withdrawals_user_created ON shared_account_withdrawals(user_id, created_at DESC);

-- Use an immutable marker instead of classifying groups by their editable names.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS is_shared_pool BOOLEAN NOT NULL DEFAULT FALSE;
CREATE UNIQUE INDEX IF NOT EXISTS idx_groups_shared_platform ON groups(platform) WHERE is_shared_pool AND deleted_at IS NULL;

ALTER TABLE proxies ADD COLUMN IF NOT EXISTS owner_user_id BIGINT REFERENCES users(id) ON DELETE CASCADE;
CREATE INDEX IF NOT EXISTS idx_proxies_owner_user ON proxies(owner_user_id) WHERE owner_user_id IS NOT NULL;
