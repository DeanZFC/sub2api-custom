ALTER TABLE group_probes
    ALTER COLUMN idle_window_minutes SET DEFAULT 5,
    ALTER COLUMN min_requests SET DEFAULT 5,
    ADD COLUMN duration_minutes integer NOT NULL DEFAULT 10 CHECK (duration_minutes BETWEEN 1 AND 1440),
    ADD COLUMN interval_seconds integer NOT NULL DEFAULT 30 CHECK (interval_seconds BETWEEN 1 AND 86400),
    ADD COLUMN round_until timestamptz;
