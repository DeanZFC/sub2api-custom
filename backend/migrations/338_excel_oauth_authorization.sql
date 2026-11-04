-- Independent encrypted Excel grants and a durable Password/TOTP login queue.
-- Primary account credentials and account scheduling are not replaced.
CREATE TABLE IF NOT EXISTS openai_oauth_reauth_configs (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    login_email TEXT NOT NULL,
    credential_mode TEXT NOT NULL DEFAULT 'password_totp' CHECK (credential_mode = 'password_totp'),
    engine TEXT NOT NULL DEFAULT 'local_worker' CHECK (engine = 'local_worker'),
    proxy_source TEXT NOT NULL DEFAULT 'account' CHECK (proxy_source IN ('account', 'managed_proxy', 'mihomo')),
    proxy_id BIGINT REFERENCES proxies(id) ON DELETE SET NULL,
    password_ciphertext TEXT,
    totp_secret_ciphertext TEXT,
    otp_url_ciphertext TEXT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    CHECK (proxy_source = 'managed_proxy' OR proxy_id IS NULL)
);

CREATE TABLE IF NOT EXISTS openai_oauth_reauth_tasks (
    id BIGSERIAL PRIMARY KEY,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued', 'running', 'callback_processing', 'succeeded', 'failed')),
    stage TEXT NOT NULL DEFAULT 'queued' CHECK (stage IN (
        'queued', 'starting', 'protocol_connecting', 'email_submitted',
        'waiting_otp', 'otp_submitted', 'password_submitted', 'mfa_submitted',
        'waiting_callback', 'exchanging_token', 'applying_credentials', 'succeeded', 'failed'
    )),
    oauth_profile TEXT NOT NULL DEFAULT 'excel' CHECK (oauth_profile = 'excel'),
    worker_id TEXT,
    auth_session_id TEXT,
    expected_credentials_hash TEXT NOT NULL,
    error_message TEXT,
    attempt INTEGER NOT NULL DEFAULT 0,
    claimed_at TIMESTAMPTZ,
    finished_at TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_openai_oauth_reauth_tasks_account_created
    ON openai_oauth_reauth_tasks (account_id, created_at DESC, id DESC);
CREATE UNIQUE INDEX IF NOT EXISTS uq_openai_oauth_reauth_tasks_active_account
    ON openai_oauth_reauth_tasks (account_id) WHERE status IN ('queued', 'running', 'callback_processing');
CREATE INDEX IF NOT EXISTS idx_openai_oauth_reauth_tasks_claimable
    ON openai_oauth_reauth_tasks (status, created_at, id);
CREATE TABLE IF NOT EXISTS openai_excel_oauth_credentials (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
    credentials_ciphertext TEXT NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
