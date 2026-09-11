CREATE TABLE IF NOT EXISTS shared_api_keys (
    id BIGSERIAL PRIMARY KEY,
    user_id BIGINT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name VARCHAR(100) NOT NULL,
    key VARCHAR(128) NOT NULL UNIQUE,
    platform VARCHAR(32) NOT NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'active',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    deleted_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_shared_api_keys_user ON shared_api_keys(user_id) WHERE deleted_at IS NULL;
CREATE TABLE IF NOT EXISTS shared_api_key_listings (
    api_key_id BIGINT NOT NULL REFERENCES shared_api_keys(id) ON DELETE CASCADE,
    listing_id BIGINT NOT NULL REFERENCES shared_account_listings(id) ON DELETE CASCADE,
    position INT NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    PRIMARY KEY (api_key_id, listing_id)
);
CREATE INDEX IF NOT EXISTS idx_shared_api_key_listings_order ON shared_api_key_listings(api_key_id, position);
