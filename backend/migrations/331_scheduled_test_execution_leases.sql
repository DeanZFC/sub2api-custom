CREATE TABLE IF NOT EXISTS scheduled_test_execution_leases (
    plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    scope TEXT NOT NULL,
    owner TEXT NOT NULL,
    expires_at TIMESTAMPTZ NOT NULL,
    PRIMARY KEY (plan_id, scope)
);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_leases_expiry ON scheduled_test_execution_leases(expires_at);
