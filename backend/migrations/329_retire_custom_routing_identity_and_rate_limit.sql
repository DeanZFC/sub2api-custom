-- Retire only the custom single-machine mode and upstream billing ceiling.
-- Keep official fingerprint modes/seeds, ordinary probe/sync preferences,
-- proxies, credentials, concurrency and all manual/quality scheduling state.
-- Historical migrations stay immutable; this cleanup is safe to run twice.
WITH changed AS (
    UPDATE accounts
    SET extra = CASE
            WHEN btrim(extra ->> 'codex_fingerprint_mode') = 'single_machine_multi_window'
            THEN jsonb_set(extra - 'upstream_billing_rate_limit', '{codex_fingerprint_mode}', '"off"'::jsonb)
            ELSE extra - 'upstream_billing_rate_limit'
        END,
        updated_at = NOW()
    WHERE jsonb_typeof(extra) = 'object'
      AND (extra ? 'upstream_billing_rate_limit'
           OR btrim(extra ->> 'codex_fingerprint_mode') = 'single_machine_multi_window')
    RETURNING id
)
INSERT INTO scheduler_outbox (event_type, account_id)
SELECT 'account_changed', id FROM changed;
