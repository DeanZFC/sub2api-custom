CREATE TABLE IF NOT EXISTS account_proxies (
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    proxy_id BIGINT NOT NULL REFERENCES proxies(id) ON DELETE CASCADE,
    position INTEGER NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (account_id, proxy_id)
);
CREATE INDEX IF NOT EXISTS idx_account_proxies_proxy_id ON account_proxies(proxy_id);
CREATE INDEX IF NOT EXISTS idx_account_proxies_account_position ON account_proxies(account_id, position);
