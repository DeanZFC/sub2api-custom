-- Retire custom ticket harvesting without changing account identity, business
-- proxies, credentials, quality policies or provider cooldowns. Historical
-- migrations remain immutable. Ignore malformed legacy non-object extra.
DELETE FROM settings WHERE starts_with(key, 'openai_codex_ticket_');

WITH retired AS (
    SELECT a.id, array_agg(e.key) AS keys
    FROM accounts a
    CROSS JOIN LATERAL jsonb_object_keys(
        CASE WHEN jsonb_typeof(a.extra) = 'object' THEN a.extra ELSE '{}'::jsonb END
    ) AS e(key)
    WHERE starts_with(e.key, 'codex_turn_ticket:')
       OR starts_with(e.key, 'codex_ticket_')
       OR e.key IN ('codex_turn_cookies', 'codex_harvest_proxy_url')
    GROUP BY a.id
), changed AS (
    UPDATE accounts a
    SET extra = a.extra - retired.keys, updated_at = NOW()
    FROM retired
    WHERE a.id = retired.id
    RETURNING a.id
)
INSERT INTO scheduler_outbox (event_type, account_id)
SELECT 'account_changed', id FROM changed;
