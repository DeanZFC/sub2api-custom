ALTER TABLE accounts
    ADD COLUMN IF NOT EXISTS rate_limit_429_retry_count INTEGER NOT NULL DEFAULT 5;

UPDATE accounts
SET rate_limit_429_retry_count = LEAST(10, GREATEST(0, rate_limit_429_retry_count))
WHERE rate_limit_429_retry_count < 0
   OR rate_limit_429_retry_count > 10;

ALTER TABLE accounts
    DROP CONSTRAINT IF EXISTS accounts_rate_limit_429_retry_count_check;

ALTER TABLE accounts
    ADD CONSTRAINT accounts_rate_limit_429_retry_count_check
    CHECK (rate_limit_429_retry_count >= 0 AND rate_limit_429_retry_count <= 10);
