SET LOCAL lock_timeout = '5s';
-- Existing records occupy generation zero. Setting the volatile default separately
-- avoids rewriting historical rows while allowing older servers to keep inserting.
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS sync_xid bigint;
ALTER TABLE usage_logs ALTER COLUMN sync_xid SET DEFAULT txid_current();
ALTER TABLE usage_logs ADD COLUMN IF NOT EXISTS http_status smallint;
