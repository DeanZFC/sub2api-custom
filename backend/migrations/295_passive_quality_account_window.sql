-- Bound all-model failure windows by account and time, without sorting every
-- model's retained failures. Keep the existing index for filtered policies.
CREATE INDEX IF NOT EXISTS passive_quality_failures_account_window
 ON passive_quality_failures(account_id,created_at DESC,id DESC);
