-- Defense-in-depth constraints for values written by shared-pool billing.
-- Service validation remains the user-facing guard; these constraints prevent
-- a malformed command or direct data repair from creating negative balances or
-- crediting more than the amount charged.
DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shared_usage_costs_nonnegative_ck') THEN
        ALTER TABLE shared_account_usage_ledger
            ADD CONSTRAINT shared_usage_costs_nonnegative_ck
            CHECK (gross_cost >= 0 AND fee_rate_percent >= 0 AND fee_rate_percent <= 100 AND platform_fee >= 0 AND owner_amount >= 0 AND platform_fee <= gross_cost AND owner_amount <= gross_cost AND platform_fee + owner_amount = gross_cost) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shared_wallet_amounts_nonnegative_ck') THEN
        ALTER TABLE shared_account_wallets
            ADD CONSTRAINT shared_wallet_amounts_nonnegative_ck
            CHECK (pending_amount >= 0 AND available_amount >= 0 AND frozen_amount >= 0 AND total_earned >= 0 AND total_transferred >= 0) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shared_wallet_ledger_amount_nonnegative_ck') THEN
        ALTER TABLE shared_account_wallet_ledger
            ADD CONSTRAINT shared_wallet_ledger_amount_nonnegative_ck CHECK (amount >= 0) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shared_api_key_listing_position_ck') THEN
        ALTER TABLE shared_api_key_listings
            ADD CONSTRAINT shared_api_key_listing_position_ck CHECK (position >= 0) NOT VALID;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'shared_api_key_status_ck') THEN
        ALTER TABLE shared_api_keys
            ADD CONSTRAINT shared_api_key_status_ck CHECK (status IN ('active', 'disabled')) NOT VALID;
    END IF;
END $$;
