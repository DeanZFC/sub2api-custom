-- Retire shared-pool resources before removing their runtime isolation fields.
-- Retain historical usage, settlements, wallet balances and legacy columns.
DO $$
DECLARE
    account_ids BIGINT[] := '{}';
    group_ids BIGINT[] := '{}';
    mapped_ids BIGINT[] := '{}';
    retired RECORD;
BEGIN
    IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('accounts')
               AND attname = 'account_scope' AND NOT attisdropped) THEN
        EXECUTE 'SELECT COALESCE(array_agg(id), ''{}''::bigint[]) FROM accounts WHERE account_scope = ''shared'''
            INTO account_ids;
    END IF;
    IF to_regclass('shared_account_listings') IS NOT NULL THEN
        EXECUTE 'SELECT COALESCE(array_agg(account_id), ''{}''::bigint[]) FROM shared_account_listings'
            INTO mapped_ids;
        account_ids := account_ids || mapped_ids;
        EXECUTE 'UPDATE shared_account_listings SET status = ''deleted'', deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
                 WHERE deleted_at IS NULL OR status <> ''deleted''';
        IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('shared_account_listings')
                   AND attname = 'listed' AND NOT attisdropped) THEN
            EXECUTE 'UPDATE shared_account_listings SET listed = FALSE WHERE listed';
        END IF;
    END IF;
    FOR retired IN
        UPDATE accounts SET status = 'disabled', schedulable = FALSE,
            deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
        WHERE id = ANY(account_ids) AND (deleted_at IS NULL OR status <> 'disabled' OR schedulable)
        RETURNING id
    LOOP
        INSERT INTO scheduler_outbox (event_type, account_id, payload)
        SELECT 'account_changed', retired.id,
               jsonb_build_object('group_ids', COALESCE(jsonb_agg(group_id), '[]'::jsonb))
        FROM account_groups WHERE account_id = retired.id;
    END LOOP;

    IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('groups')
               AND attname = 'is_shared_pool' AND NOT attisdropped) THEN
        EXECUTE 'SELECT COALESCE(array_agg(id), ''{}''::bigint[]) FROM groups WHERE is_shared_pool'
            INTO group_ids;
    END IF;
    FOR retired IN
        UPDATE groups SET status = 'disabled', deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
        WHERE id = ANY(group_ids) AND (deleted_at IS NULL OR status <> 'disabled')
        RETURNING id
    LOOP
        INSERT INTO scheduler_outbox (event_type, group_id) VALUES ('group_changed', retired.id);
    END LOOP;

    IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('proxies')
               AND attname = 'owner_user_id' AND NOT attisdropped) THEN
        EXECUTE 'UPDATE proxies SET status = ''disabled'', deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
                 WHERE owner_user_id IS NOT NULL AND (deleted_at IS NULL OR status <> ''disabled'')';
    END IF;

    -- Normal API-key UPDATE triggers invalidate cached authorization as well.
    UPDATE api_keys SET status = 'disabled', deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
    WHERE (key LIKE 'sk-shared-%' OR group_id = ANY(group_ids))
        AND (deleted_at IS NULL OR status <> 'disabled');
    IF to_regclass('shared_api_keys') IS NOT NULL THEN
        EXECUTE 'UPDATE api_keys k SET status = ''disabled'', deleted_at = COALESCE(k.deleted_at, NOW()), updated_at = NOW()
                 FROM shared_api_keys s WHERE k.key = s.key AND (k.deleted_at IS NULL OR k.status <> ''disabled'')';
        IF EXISTS (SELECT 1 FROM pg_attribute WHERE attrelid = to_regclass('shared_api_keys')
                   AND attname = 'legacy_api_key_id' AND NOT attisdropped) THEN
            EXECUTE 'UPDATE api_keys k SET status = ''disabled'', deleted_at = COALESCE(k.deleted_at, NOW()), updated_at = NOW()
                     FROM shared_api_keys s WHERE k.id = s.legacy_api_key_id AND (k.deleted_at IS NULL OR k.status <> ''disabled'')';
        END IF;
        EXECUTE 'UPDATE shared_api_keys SET status = ''disabled'', deleted_at = COALESCE(deleted_at, NOW()), updated_at = NOW()
                 WHERE deleted_at IS NULL OR status <> ''disabled''';
    END IF;
END $$;

DELETE FROM settings WHERE key IN (
    'shared_pool_enabled', 'shared_pool_fee_rate_percent'
);
