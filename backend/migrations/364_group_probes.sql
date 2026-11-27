-- Private admin probes. Neither customer usage nor the public channel monitor reads these tables.
CREATE TABLE group_probes (
    group_id BIGINT PRIMARY KEY REFERENCES groups(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    enabled BOOLEAN NOT NULL,
    manual_requested BOOLEAN NOT NULL DEFAULT false,
    next_poll_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    lease_owner UUID,
    lease_until TIMESTAMPTZ,
    last_user_at TIMESTAMPTZ,
    last_probe_at TIMESTAMPTZ,
    last_result JSONB,
    failures INT NOT NULL DEFAULT 0,
    deferred_until TIMESTAMPTZ,
    deferred_reason TEXT NOT NULL DEFAULT ''
);
CREATE INDEX group_probes_due ON group_probes(next_poll_at) WHERE enabled;

CREATE TABLE group_probe_runs (
    id BIGSERIAL PRIMARY KEY,
    group_id BIGINT NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    model VARCHAR(200) NOT NULL,
    checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    result JSONB NOT NULL
);
CREATE INDEX group_probe_runs_recent ON group_probe_runs(group_id, checked_at DESC);
