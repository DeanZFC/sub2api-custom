-- Include nonblocking observations in the per-account quality overview.
CREATE INDEX IF NOT EXISTS idx_st_protection_account
    ON scheduled_test_protection_states(account_id);
