-- A global cap dedicated to visual-judge calls, including confirmation calls.
-- Existing quality request/token budgets continue to apply independently.
CREATE TABLE IF NOT EXISTS scheduled_test_auto_review_days (
    day DATE PRIMARY KEY,
    requests BIGINT NOT NULL DEFAULT 0 CHECK (requests >= 0)
);
