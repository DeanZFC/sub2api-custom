ALTER TABLE group_probes
    ADD COLUMN idle_window_minutes integer NOT NULL DEFAULT 10 CHECK (idle_window_minutes BETWEEN 1 AND 1440),
    ADD COLUMN min_requests integer NOT NULL DEFAULT 1 CHECK (min_requests > 0);
