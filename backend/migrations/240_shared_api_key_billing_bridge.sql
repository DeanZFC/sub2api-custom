ALTER TABLE shared_api_keys
    ADD COLUMN IF NOT EXISTS legacy_api_key_id BIGINT REFERENCES api_keys(id) ON DELETE SET NULL;
CREATE UNIQUE INDEX IF NOT EXISTS idx_shared_api_keys_legacy_api_key
    ON shared_api_keys(legacy_api_key_id) WHERE legacy_api_key_id IS NOT NULL;

-- Backfill compatibility rows for keys created before this migration. The
-- rows are hidden from ordinary API-key queries by their reserved prefix.
INSERT INTO api_keys(user_id, key, name, status)
SELECT s.user_id, s.key, '[shared] ' || s.name, CASE WHEN s.status = 'active' THEN 'active' ELSE 'disabled' END
FROM shared_api_keys s
WHERE s.legacy_api_key_id IS NULL
ON CONFLICT (key) DO NOTHING;
UPDATE shared_api_keys s
SET legacy_api_key_id = k.id
FROM api_keys k
WHERE s.legacy_api_key_id IS NULL AND k.key = s.key;
