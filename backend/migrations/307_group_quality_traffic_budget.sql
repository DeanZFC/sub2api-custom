-- Additive and rollback-compatible. Old binaries ignore these tables.
-- Every public model in one group shares this budget; verification is isolated.
CREATE TABLE group_quality_traffic_snapshots (
 policy_id BIGINT PRIMARY KEY REFERENCES passive_quality_policies(id) ON DELETE CASCADE,
 policy_version BIGINT NOT NULL,
 input_tokens BIGINT NOT NULL DEFAULT 0 CHECK(input_tokens >= 0),
 read_tokens BIGINT NOT NULL DEFAULT 0 CHECK(read_tokens >= 0),
 telemetry_complete BOOLEAN NOT NULL DEFAULT false,
 measured_at TIMESTAMPTZ,
 last_fallback_at TIMESTAMPTZ,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE group_quality_traffic_leases (
 lease_id TEXT NOT NULL,
 policy_id BIGINT NOT NULL REFERENCES passive_quality_policies(id) ON DELETE CASCADE,
 policy_version BIGINT NOT NULL,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 group_id BIGINT NOT NULL,
 model TEXT NOT NULL,
 request_key TEXT NOT NULL,
 validation BOOLEAN NOT NULL,
 fallback BOOLEAN NOT NULL DEFAULT false,
 fallback_expires_at TIMESTAMPTZ,
 reserved_input BIGINT NOT NULL CHECK(reserved_input >= 0),
 input_tokens BIGINT CHECK(input_tokens >= 0),
 read_tokens BIGINT CHECK(read_tokens >= 0 AND read_tokens <= input_tokens),
 cache_reported BOOLEAN NOT NULL DEFAULT false,
 unused BOOLEAN NOT NULL DEFAULT false,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 released_at TIMESTAMPTZ,
 settled_at TIMESTAMPTZ,
 PRIMARY KEY(lease_id, policy_id),
 CHECK(NOT fallback OR fallback_expires_at IS NOT NULL)
);
CREATE INDEX group_quality_traffic_window ON group_quality_traffic_leases(policy_id, created_at);
CREATE INDEX group_quality_traffic_retention ON group_quality_traffic_leases(created_at);
CREATE INDEX group_quality_fallback_live ON group_quality_traffic_leases(group_id,fallback_expires_at) WHERE fallback AND released_at IS NULL;
CREATE TABLE group_quality_traffic_denials (
 policy_id BIGINT NOT NULL REFERENCES passive_quality_policies(id) ON DELETE CASCADE,
 minute TIMESTAMPTZ NOT NULL,
 requests BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(policy_id, minute)
);
