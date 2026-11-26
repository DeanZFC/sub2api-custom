-- A quality strategy calls an upstream account directly, without a customer or API key.
SET LOCAL lock_timeout = '5s';
ALTER TABLE usage_logs
    ALTER COLUMN user_id DROP NOT NULL,
    ALTER COLUMN api_key_id DROP NOT NULL,
    ADD CONSTRAINT usage_logs_actor_check CHECK (
        (user_id IS NOT NULL AND api_key_id IS NOT NULL) OR
        (user_id IS NULL AND api_key_id IS NULL AND inbound_endpoint IS NOT DISTINCT FROM '/internal/channel-quality')
    ) NOT VALID;

-- Shared group memberships are immutable snapshots. The monitor encodes
-- multi-group scopes as -id in its minute and rollup dimension keys, so a
-- request contributes once even when several selected groups match.
CREATE TABLE channel_quality_group_sets (
    id BIGSERIAL PRIMARY KEY,
    group_ids BIGINT[] NOT NULL UNIQUE CHECK (cardinality(group_ids) >= 2)
);
ALTER TABLE usage_logs
    ADD COLUMN quality_group_set_id BIGINT,
    ADD COLUMN quality_usage JSONB;
ALTER TABLE ops_error_logs ADD COLUMN quality_group_set_id BIGINT;
ALTER TABLE quality_probe_attempts ADD COLUMN usage_recorded BOOLEAN NOT NULL DEFAULT FALSE;
