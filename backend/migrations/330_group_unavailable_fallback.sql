-- Administrator-owned capacity fallback. Billing stays with the source group;
-- this is independent of API-key fallback, which changes the billing group.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS fallback_group_id_on_unavailable BIGINT
    REFERENCES groups(id) ON DELETE SET NULL;
ALTER TABLE groups ADD CONSTRAINT groups_unavailable_fallback_not_self
    CHECK (fallback_group_id_on_unavailable IS NULL OR fallback_group_id_on_unavailable <> id);
CREATE INDEX IF NOT EXISTS idx_groups_unavailable_fallback
    ON groups(fallback_group_id_on_unavailable)
    WHERE deleted_at IS NULL AND fallback_group_id_on_unavailable IS NOT NULL;
COMMENT ON COLUMN groups.fallback_group_id_on_unavailable IS
    'All accounts unavailable: use fallback accounts while billing the original group';
