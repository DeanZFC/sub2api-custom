-- Opt-in v2 policies keep availability, latency and cache allocation independent.
-- Existing JSON policies/states deliberately retain their original semantics.
-- Only request-time digests and scope metadata are retained, never prompt text.
ALTER TABLE passive_quality_cache_reports
 ADD COLUMN IF NOT EXISTS cache_metadata JSONB NOT NULL DEFAULT '{}'::jsonb;
CREATE INDEX IF NOT EXISTS passive_quality_cache_reuse_window
 ON passive_quality_cache_reports
 (account_id, (cache_metadata->>'session_hash'), created_at DESC)
 WHERE cache_metadata->>'session_hash' IS NOT NULL;
CREATE INDEX IF NOT EXISTS passive_quality_cache_session_window
 ON passive_quality_cache_reports ((cache_metadata->>'session_hash'), created_at DESC)
 WHERE cache_metadata->>'session_hash' IS NOT NULL;
