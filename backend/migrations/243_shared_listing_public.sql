ALTER TABLE shared_account_listings
    ADD COLUMN IF NOT EXISTS listed BOOLEAN NOT NULL DEFAULT TRUE;

CREATE INDEX IF NOT EXISTS idx_shared_listing_listed_status
    ON shared_account_listings(platform, status)
    WHERE deleted_at IS NULL AND listed;
