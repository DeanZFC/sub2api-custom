-- Shared account pool was added after existing installations had already
-- initialized their settings. Seed the feature flag for those databases while
-- preserving an explicit operator choice when the key already exists.
INSERT INTO settings (key, value, updated_at)
VALUES ('shared_pool_enabled', 'true', NOW())
ON CONFLICT (key) DO NOTHING;
