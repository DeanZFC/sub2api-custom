-- Restore optional Codex ticket harvesting after the immutable retirement in 269.
-- Settings and old expired ticket values are not recovered. Operators opt in again.
-- The learning tables store redacted node statistics; flow rows never hold tokens.

CREATE TABLE IF NOT EXISTS codex_harvest_learning_epoch (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    generation BIGINT NOT NULL DEFAULT 1
);
INSERT INTO codex_harvest_learning_epoch(id, generation) VALUES (1, 1) ON CONFLICT DO NOTHING;

CREATE TABLE IF NOT EXISTS codex_harvest_nodes (
    id BIGSERIAL PRIMARY KEY,
    pool_id VARCHAR(64) NOT NULL,
    node_id VARCHAR(64) NOT NULL,
    node_name VARCHAR(512) NOT NULL,
    provider VARCHAR(512) NOT NULL,
    account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    identity VARCHAR(64) NOT NULL,
    model VARCHAR(200) NOT NULL,
    blocks INTEGER NOT NULL,
    successes BIGINT NOT NULL DEFAULT 0,
    misses BIGINT NOT NULL DEFAULT 0,
    network_errors BIGINT NOT NULL DEFAULT 0,
    account_errors BIGINT NOT NULL DEFAULT 0,
    consecutive_failures INTEGER NOT NULL DEFAULT 0,
    last_success TIMESTAMPTZ,
    cooldown_until TIMESTAMPTZ,
    latency_ms BIGINT NOT NULL DEFAULT 0,
    last_result VARCHAR(40) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (pool_id, node_id, account_id, identity, model, blocks)
);
CREATE INDEX IF NOT EXISTS idx_codex_harvest_nodes_scope
    ON codex_harvest_nodes (pool_id, account_id, identity, model, blocks);
CREATE INDEX IF NOT EXISTS idx_codex_harvest_nodes_updated
    ON codex_harvest_nodes (updated_at DESC, id DESC);

CREATE TABLE IF NOT EXISTS codex_harvest_flow_events (
    id BIGSERIAL PRIMARY KEY,
    event_id VARCHAR(64) NOT NULL UNIQUE,
    at TIMESTAMPTZ NOT NULL,
    stage VARCHAR(32) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL DEFAULT '',
    account_id BIGINT NOT NULL DEFAULT 0,
    account_name VARCHAR(80) NOT NULL DEFAULT '',
    model VARCHAR(64) NOT NULL DEFAULT '',
    node VARCHAR(160) NOT NULL DEFAULT '',
    http_status INTEGER NOT NULL DEFAULT 0,
    length INTEGER NOT NULL DEFAULT 0,
    blocks INTEGER NOT NULL DEFAULT 0,
    expected_length INTEGER NOT NULL DEFAULT 0,
    expected_blocks INTEGER NOT NULL DEFAULT 0,
    accepted BOOLEAN NOT NULL DEFAULT FALSE,
    standby BOOLEAN NOT NULL DEFAULT FALSE,
    result VARCHAR(64) NOT NULL DEFAULT '',
    reason VARCHAR(64) NOT NULL DEFAULT '',
    detail VARCHAR(240) NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_codex_harvest_flow_events_at
    ON codex_harvest_flow_events (at ASC, id ASC);

INSERT INTO settings(key, value, updated_at)
VALUES ('openai_codex_ticket_enabled', 'false', NOW()),
       ('openai_codex_ticket_fail_closed', 'false', NOW())
ON CONFLICT (key) DO NOTHING;
