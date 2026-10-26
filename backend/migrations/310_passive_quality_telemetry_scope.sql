-- Keep the original one-column table stable for rolling upgrades: old writers
-- insert positional VALUES($1) and therefore cannot tolerate added columns.
-- Legacy markers remain global; new writers use this scoped interval table.
CREATE TABLE passive_quality_telemetry_gap_scopes (
 id BIGSERIAL PRIMARY KEY,
 created_at TIMESTAMPTZ NOT NULL,
 ended_at TIMESTAMPTZ NOT NULL,
 account_id BIGINT NOT NULL DEFAULT 0,
 group_id BIGINT NOT NULL DEFAULT 0,
 model TEXT NOT NULL DEFAULT '',
 kind TEXT NOT NULL DEFAULT 'all',
 dropped_records BIGINT NOT NULL DEFAULT 0
);
CREATE INDEX passive_quality_telemetry_gap_scopes_window
 ON passive_quality_telemetry_gap_scopes(created_at,ended_at);
