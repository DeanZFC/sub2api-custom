-- Retire the experimental channel and the separate account-protection policies.
-- Only the explicit single-machine fingerprint remains as a custom identity mode.
-- This migration is transactional and repeatable. It never clears official
-- rate-limit/overload state or unrelated temporary scheduling blocks.
DO $$
DECLARE
    a RECORD;
    next_extra JSONB;
    previous JSONB;
    marker JSONB;
    config_key TEXT;
    protection_enabled BOOLEAN;
    identity_protection BOOLEAN;
    next_concurrency INTEGER;
    seed_bytes BYTEA;
BEGIN
    FOR a IN
        SELECT id, platform, type, extra, credentials, concurrency,
               temp_unschedulable_reason
        FROM accounts
        WHERE extra ?| ARRAY['prism', 'anti_degrade', 'anti_degradation',
                             'protection_scope', 'account_protection_policy',
                             'request_integrity_mode', 'tls_fingerprint_builtin']
           OR credentials ?| ARRAY['prism_cookie', 'prism_cookie_configured']
           OR extra ->> 'codex_fingerprint_mode' = 'account_device'
           OR temp_unschedulable_reason LIKE 'health:%'
        FOR UPDATE
    LOOP
        next_extra := COALESCE(a.extra, '{}'::jsonb);
        marker := next_extra -> 'anti_degrade';
        previous := marker -> 'prev';
        protection_enabled := COALESCE(next_extra -> 'anti_degradation',
                                       marker -> 'enabled', 'false'::jsonb) = 'true'::jsonb;
        identity_protection := protection_enabled
            AND COALESCE(marker ->> 'mode', 'legacy') <> 'generic';
        next_concurrency := a.concurrency;

        IF protection_enabled AND jsonb_typeof(previous) = 'object' THEN
            -- Restore an automatically imposed cap only if the administrator
            -- has not changed it since protection was enabled.
            IF jsonb_typeof(previous -> 'concurrency') = 'number'
               AND previous ->> 'concurrency' ~ '^-?[0-9]+$'
               AND (previous ->> 'concurrency')::numeric BETWEEN -2147483648 AND 2147483647
               AND to_jsonb(a.concurrency) = COALESCE(marker -> 'applied_concurrency', marker -> 'max_concurrency') THEN
                next_concurrency := (previous ->> 'concurrency')::integer;
            END IF;
            IF identity_protection THEN
                FOREACH config_key IN ARRAY ARRAY['codex_fingerprint_mode', 'enable_tls_fingerprint',
                                                   'tls_fingerprint_profile_id', 'proxy_mode']
                LOOP
                    IF previous ? config_key THEN
                        IF previous -> config_key = 'null'::jsonb THEN
                            next_extra := next_extra - config_key;
                        ELSE
                            next_extra := jsonb_set(next_extra, ARRAY[config_key], previous -> config_key);
                        END IF;
                    END IF;
                END LOOP;
            END IF;
        END IF;

        IF identity_protection AND a.platform = 'openai' AND a.type IN ('oauth', 'setup-token') THEN
            next_extra := jsonb_set(next_extra, '{codex_fingerprint_mode}', '"single_machine_multi_window"');
        END IF;

        IF next_extra ->> 'codex_fingerprint_mode' IN ('account_device', 'single_machine_multi_window')
           AND a.platform = 'openai' AND a.type IN ('oauth', 'setup-token') THEN
            -- Preserve the old account-derived device identity if no stored
            -- seed exists. This matches deriveAccountCodexFingerprintSeed.
            IF COALESCE(next_extra ->> 'codex_fingerprint_seed', '') !~
               '^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$'
               OR next_extra ->> 'codex_fingerprint_seed' = '00000000-0000-0000-0000-000000000000' THEN
                seed_bytes := substring(sha256(convert_to('sub2api:openai-account-fingerprint:v1:' || a.id::text, 'UTF8')) FROM 1 FOR 16);
                seed_bytes := set_byte(seed_bytes, 6, (get_byte(seed_bytes, 6) & 15) | 64);
                seed_bytes := set_byte(seed_bytes, 8, (get_byte(seed_bytes, 8) & 63) | 128);
                next_extra := jsonb_set(next_extra, '{codex_fingerprint_seed}', to_jsonb(encode(seed_bytes, 'hex')::uuid::text));
            END IF;
        END IF;
        IF next_extra ->> 'codex_fingerprint_mode' = 'account_device' THEN
            next_extra := jsonb_set(next_extra, '{codex_fingerprint_mode}', '"device"');
        END IF;

        next_extra := next_extra - ARRAY['prism', 'anti_degrade', 'anti_degradation',
                                         'protection_scope', 'account_protection_policy',
                                         'request_integrity_mode', 'tls_fingerprint_builtin'];
        UPDATE accounts
        SET extra = next_extra,
            credentials = credentials - ARRAY['prism_cookie', 'prism_cookie_configured'],
            concurrency = next_concurrency,
            temp_unschedulable_until = CASE WHEN temp_unschedulable_reason LIKE 'health:%' THEN NULL ELSE temp_unschedulable_until END,
            temp_unschedulable_reason = CASE WHEN temp_unschedulable_reason LIKE 'health:%' THEN NULL ELSE temp_unschedulable_reason END,
            updated_at = NOW()
        WHERE id = a.id;

        INSERT INTO scheduler_outbox (event_type, account_id)
        VALUES ('account_changed', a.id);
    END LOOP;
END $$;

DELETE FROM settings WHERE key = 'account_health_settings';
