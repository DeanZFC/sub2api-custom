-- Keep historical results while revoking automatic actions after a manual
-- account move out of a policy. Late responses must not recreate old holds.
ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS protection_revoked BOOLEAN NOT NULL DEFAULT FALSE;
