-- Cost admission is separate from retained test results and account health.
CREATE TABLE IF NOT EXISTS quality_operations_config (
 id integer PRIMARY KEY CHECK (id=1), version bigint NOT NULL DEFAULT 1,
 config jsonb NOT NULL, updated_at timestamptz NOT NULL DEFAULT now()
);
INSERT INTO quality_operations_config(id,config) VALUES(1,'{"budget_enabled":false,"daily_requests":10000,"daily_tokens":10000000,"account_daily_requests":200,"account_daily_tokens":1000000,"token_reservation":32768,"dynamic_enabled":false,"healthy_rounds":3,"healthy_base_seconds":900,"healthy_max_seconds":14400}') ON CONFLICT DO NOTHING;
CREATE TABLE IF NOT EXISTS quality_budget_days (
 day date NOT NULL, account_id bigint NOT NULL, requests bigint NOT NULL DEFAULT 0,
 tokens bigint NOT NULL DEFAULT 0, unknown_requests bigint NOT NULL DEFAULT 0,
 PRIMARY KEY(day,account_id)
);
CREATE TABLE IF NOT EXISTS quality_suppliers (
 id bigserial PRIMARY KEY, version bigint NOT NULL DEFAULT 1, config jsonb NOT NULL,
 updated_at timestamptz NOT NULL DEFAULT now(), next_probe_at timestamptz,
 probe_lease text NOT NULL DEFAULT ''
);
CREATE TABLE IF NOT EXISTS quality_supplier_accounts (
 account_id bigint PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 supplier_id bigint NOT NULL REFERENCES quality_suppliers(id) ON DELETE CASCADE
);
CREATE INDEX IF NOT EXISTS idx_quality_supplier_members ON quality_supplier_accounts(supplier_id);
CREATE TABLE IF NOT EXISTS quality_probe_attempts (
 id bigserial PRIMARY KEY, account_id bigint NOT NULL, day date NOT NULL,
 supplier_id bigint, reserved_tokens bigint NOT NULL, actual_tokens bigint,
 settled boolean NOT NULL DEFAULT false, kind text NOT NULL DEFAULT 'unknown',
 started_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_quality_probe_retention ON quality_probe_attempts(started_at);
CREATE TABLE IF NOT EXISTS quality_probe_outcomes (
 account_id bigint PRIMARY KEY REFERENCES accounts(id) ON DELETE CASCADE,
 supplier_id bigint, attempt_id bigint NOT NULL, kind text NOT NULL,
 observed_at timestamptz NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_quality_probe_supplier ON quality_probe_outcomes(supplier_id,observed_at);
CREATE TABLE IF NOT EXISTS quality_healthy_frequency (
 plan_id bigint NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
 account_id bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 policy_key text NOT NULL, config_version bigint NOT NULL, streak integer NOT NULL DEFAULT 0,
 next_at timestamptz NOT NULL, observed_at timestamptz NOT NULL,
 PRIMARY KEY(plan_id,account_id)
);
