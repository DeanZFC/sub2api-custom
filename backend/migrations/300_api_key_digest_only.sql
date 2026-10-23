-- Irreversible credential migration. Upgrade all application instances together;
-- older binaries that compare plaintext keys cannot authenticate after this migration.
-- Existing client credentials remain valid; only their stored representation changes.
CREATE OR REPLACE FUNCTION api_key_cache_digest(stored_key TEXT)
RETURNS TEXT LANGUAGE sql IMMUTABLE STRICT AS $$
    SELECT CASE WHEN stored_key ~ '^\$sha256\$[0-9a-f]{64}\$'
        THEN substring(stored_key FROM 9 FOR 64)
        ELSE encode(sha256(convert_to(stored_key, 'UTF8')), 'hex') END;
$$;

CREATE OR REPLACE FUNCTION enqueue_auth_cache_invalidation(raw_key TEXT)
RETURNS VOID
LANGUAGE plpgsql
AS $$
BEGIN
    IF raw_key IS NULL OR raw_key = '' THEN
        RETURN;
    END IF;
    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    VALUES (api_key_cache_digest(raw_key));
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_api_key_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
BEGIN
    IF TG_OP = 'DELETE' THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        RETURN OLD;
    END IF;

    IF OLD.key IS DISTINCT FROM NEW.key
       OR OLD.status IS DISTINCT FROM NEW.status
       OR OLD.deleted_at IS DISTINCT FROM NEW.deleted_at
       OR OLD.user_id IS DISTINCT FROM NEW.user_id
       OR OLD.group_id IS DISTINCT FROM NEW.group_id
       OR OLD.ip_whitelist IS DISTINCT FROM NEW.ip_whitelist
       OR OLD.ip_blacklist IS DISTINCT FROM NEW.ip_blacklist
       OR OLD.expires_at IS DISTINCT FROM NEW.expires_at THEN
        PERFORM enqueue_auth_cache_invalidation(OLD.key);
        IF NEW.deleted_at IS NULL AND NEW.key IS DISTINCT FROM OLD.key THEN
            PERFORM enqueue_auth_cache_invalidation(NEW.key);
        END IF;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_user_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
BEGIN
    target_user_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.role IS NOT DISTINCT FROM NEW.role
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT api_key_cache_digest(k.key)
    FROM api_keys AS k
    WHERE k.user_id = target_user_id
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;

CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT api_key_cache_digest(k.key)
    FROM api_keys AS k
    WHERE (k.group_id = target_group_id OR k.fallback_group_id = target_group_id)
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
CREATE OR REPLACE FUNCTION enqueue_allowed_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_user_id BIGINT;
    target_group_id BIGINT;
BEGIN
    IF TG_OP = 'UPDATE'
       AND (OLD.user_id IS DISTINCT FROM NEW.user_id
            OR OLD.group_id IS DISTINCT FROM NEW.group_id) THEN
        IF EXISTS (
            SELECT 1 FROM groups g
            WHERE g.id = OLD.group_id AND g.is_exclusive = TRUE
        ) THEN
            INSERT INTO auth_cache_invalidation_outbox (cache_key)
            SELECT api_key_cache_digest(k.key)
            FROM api_keys AS k
            WHERE k.user_id = OLD.user_id
              AND k.group_id = OLD.group_id
              AND k.deleted_at IS NULL
              AND k.key <> '';
        END IF;
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSIF TG_OP = 'UPDATE' THEN
        RETURN NEW;
    ELSIF TG_OP = 'INSERT' THEN
        target_user_id := NEW.user_id;
        target_group_id := NEW.group_id;
    ELSE
        target_user_id := OLD.user_id;
        target_group_id := OLD.group_id;
    END IF;

    IF EXISTS (
        SELECT 1 FROM groups g
        WHERE g.id = target_group_id AND g.is_exclusive = TRUE
    ) THEN
        INSERT INTO auth_cache_invalidation_outbox (cache_key)
        SELECT api_key_cache_digest(k.key)
        FROM api_keys AS k
        WHERE k.user_id = target_user_id
          AND k.group_id = target_group_id
          AND k.deleted_at IS NULL
          AND k.key <> '';
    END IF;
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;


UPDATE api_keys
SET key = '$sha256$' || encode(sha256(convert_to(key, 'UTF8')), 'hex') || '$' ||
    CASE WHEN length(key) > 12 THEN left(key, 6) || '...' || right(key, 4) ELSE '***' END
WHERE key !~ '^\$sha256\$[0-9a-f]{64}\$';

-- Keep the idempotency claims, removing historical credential-bearing responses.
UPDATE idempotency_records SET response_body = '{}'
WHERE scope IN ('user.api_keys.create', 'user.api_keys.regenerate');

DO $$ BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_constraint WHERE conname = 'api_keys_digest_only' AND conrelid = 'api_keys'::regclass) THEN
        ALTER TABLE api_keys ADD CONSTRAINT api_keys_digest_only CHECK (
            key ~ '^\$sha256\$[0-9a-f]{64}\$' OR (deleted_at IS NOT NULL AND key LIKE '__deleted__%')
        );
    END IF;
END $$;

COMMENT ON COLUMN api_keys.key IS 'Versioned SHA-256 digest plus display mask; never a usable API credential';
