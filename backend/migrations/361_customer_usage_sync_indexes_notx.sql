CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_sync_user ON usage_logs (user_id, COALESCE(sync_xid, 0), id);
CREATE INDEX CONCURRENTLY IF NOT EXISTS idx_usage_sync_key ON usage_logs (api_key_id, COALESCE(sync_xid, 0), id);
