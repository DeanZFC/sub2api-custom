-- Shared-pool moderation controls. Defaults preserve existing publishers.
ALTER TABLE users ADD COLUMN IF NOT EXISTS shared_publish_enabled BOOLEAN NOT NULL DEFAULT TRUE;
ALTER TABLE users ADD COLUMN IF NOT EXISTS shared_publish_block_reason TEXT NOT NULL DEFAULT '';
ALTER TABLE users ADD COLUMN IF NOT EXISTS shared_publish_blocked_until TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS idx_users_shared_publish_enabled ON users(shared_publish_enabled, shared_publish_blocked_until);
