-- Ported from ranxi2001/sub2api production 981f3645a6df2a099b8dde1acdbbb8a77875a68a.
-- Append-only custom migration: previously applied migration files stay immutable.
ALTER TABLE proxies ADD COLUMN IF NOT EXISTS mihomo_node_id varchar(128);

CREATE UNIQUE INDEX IF NOT EXISTS idx_proxies_mihomo_node_id_active
    ON proxies (mihomo_node_id)
    WHERE mihomo_node_id IS NOT NULL AND deleted_at IS NULL;
