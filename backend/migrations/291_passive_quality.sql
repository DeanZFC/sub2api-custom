-- Passive protection owns its holds independently of scheduled tests and manual stops.
CREATE TABLE passive_quality_policies (
 id BIGSERIAL PRIMARY KEY,
 config JSONB NOT NULL,
 version BIGINT NOT NULL DEFAULT 1,
 last_scanned_at TIMESTAMPTZ NOT NULL DEFAULT '-infinity',
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE TABLE passive_quality_states (
 policy_id BIGINT NOT NULL REFERENCES passive_quality_policies(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 data JSONB NOT NULL,
 PRIMARY KEY (policy_id, account_id)
);
CREATE INDEX passive_quality_states_account ON passive_quality_states(account_id);
CREATE TABLE passive_quality_events (
 id BIGSERIAL PRIMARY KEY,
 policy_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL,
 policy_name TEXT NOT NULL,
 kind TEXT NOT NULL,
 detail JSONB NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX passive_quality_events_recent ON passive_quality_events(id DESC);
CREATE INDEX passive_quality_events_account ON passive_quality_events(account_id,id DESC);
-- A lease is never silently freed while the original request could still run.
-- The trial deadline bounds admissions even after process death.
CREATE TABLE passive_quality_leases (
 id TEXT NOT NULL,
 policy_id BIGINT NOT NULL,
 account_id BIGINT NOT NULL,
 generation BIGINT NOT NULL,
 PRIMARY KEY(id,policy_id),
 FOREIGN KEY(policy_id,account_id) REFERENCES passive_quality_states(policy_id,account_id) ON DELETE CASCADE
);
CREATE INDEX passive_quality_leases_state ON passive_quality_leases(policy_id,account_id,generation);
-- Minimal failure evidence is independent of the optional ops log feature.
-- No prompt, credential, response body or client-supplied message is stored.
CREATE TABLE passive_quality_failures (
 id BIGSERIAL PRIMARY KEY,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 model TEXT NOT NULL,
 request_key TEXT NOT NULL,
 started_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL,
 UNIQUE(account_id,request_key)
);
CREATE INDEX passive_quality_failures_window ON passive_quality_failures(account_id,model,created_at DESC);
-- usage_logs uses the existing idx_usage_logs_account_created_at index.
CREATE TABLE passive_quality_telemetry_gaps (created_at TIMESTAMPTZ PRIMARY KEY);

-- Explicit cache reporting distinguishes zero from an omitted counter. WS turn
-- identity correlates failed attempts with usage IDs without changing billing.
CREATE TABLE passive_quality_cache_reports (
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 request_key TEXT NOT NULL,
 attempt_key TEXT NOT NULL DEFAULT '',
 cache_reported BOOLEAN NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL,
 PRIMARY KEY(account_id,request_key)
);
CREATE INDEX passive_quality_cache_reports_retention ON passive_quality_cache_reports(created_at);
