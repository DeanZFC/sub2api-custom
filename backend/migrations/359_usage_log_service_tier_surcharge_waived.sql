ALTER TABLE usage_logs
    ADD COLUMN IF NOT EXISTS service_tier_surcharge_waived BOOLEAN NOT NULL DEFAULT FALSE;

COMMENT ON COLUMN usage_logs.service_tier_surcharge_waived IS
    'The group billed this Fast or Ultra Fast request at the equivalent Standard price';
