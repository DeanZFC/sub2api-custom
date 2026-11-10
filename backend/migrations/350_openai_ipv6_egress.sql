-- Fixed per-account IPv6 egress; opt-in, no existing routes changed.
CREATE TABLE openai_ipv6_bindings (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE RESTRICT,
    managed_proxy_id BIGINT NOT NULL UNIQUE REFERENCES proxies(id) ON DELETE RESTRICT,
    original_proxy_id BIGINT,
    original_proxy_ids JSONB NOT NULL DEFAULT '[]',
    original_fallback_id BIGINT,
    lease_id TEXT NOT NULL UNIQUE,
    address TEXT NOT NULL DEFAULT '',
    control_url TEXT NOT NULL,
    status TEXT NOT NULL DEFAULT 'provisioning',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Protect the route in the database as imports, batch edits and expiry sweeps
-- also write these tables. Unbinding deletes the binding in the same transaction
-- that restores the original route. Ordinary metadata/credential edits still work.
CREATE FUNCTION guard_openai_ipv6_account() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE managed BIGINT; owner_id BIGINT; parent_proxy BIGINT; parent_fallback BIGINT;
BEGIN
    IF TG_OP='INSERT' AND NEW.parent_account_id IS NOT NULL THEN
        -- Serialize shadow creation with lifecycle changes on its parent. Read
        -- the current route after the lock, never a caller's stale snapshot.
        SELECT proxy_id,proxy_fallback_origin_id INTO parent_proxy,parent_fallback
        FROM accounts WHERE id=NEW.parent_account_id FOR UPDATE;
        NEW.proxy_id := parent_proxy;
        NEW.proxy_fallback_origin_id := parent_fallback;
        NEW.extra := COALESCE(NEW.extra,'{}'::jsonb)-'openai_ipv6_bound';
    END IF;
    owner_id := COALESCE(NEW.parent_account_id, NEW.id);
    IF EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE managed_proxy_id=NEW.proxy_id AND account_id<>owner_id) THEN
        RAISE EXCEPTION 'A managed IPv6 proxy belongs to one account family';
    END IF;
    SELECT managed_proxy_id INTO managed FROM openai_ipv6_bindings WHERE account_id=owner_id;
    IF managed IS NOT NULL THEN
        IF TG_OP='INSERT' THEN
            NEW.proxy_id := managed;
            NEW.extra := COALESCE(NEW.extra,'{}'::jsonb) || '{"openai_ipv6_bound":true}'::jsonb;
            IF NOT EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE account_id=owner_id AND status='ready') THEN NEW.status := 'inactive'; END IF;
        ELSIF NEW.proxy_id IS DISTINCT FROM managed
            OR NEW.deleted_at IS NOT NULL OR NEW.platform <> 'openai' OR NEW.type <> 'oauth'
            OR NEW.parent_account_id IS DISTINCT FROM OLD.parent_account_id
            OR NEW.proxy_fallback_origin_id IS NOT NULL
            OR NEW.extra->>'openai_ipv6_bound' IS DISTINCT FROM 'true'
            OR NEW.extra->>'openai_vm_bound'='true' OR NEW.extra->>'openai_excel_bps'='true'
            OR (NEW.status='active' AND NOT EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE account_id=owner_id AND status='ready')) THEN
            RAISE EXCEPTION 'Detach the fixed IPv6 binding before changing or deleting its account route';
        END IF;
    ELSIF TG_OP='UPDATE' AND OLD.parent_account_id IS DISTINCT FROM NEW.parent_account_id
        AND EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE account_id=COALESCE(OLD.parent_account_id,OLD.id)) THEN
        RAISE EXCEPTION 'Detach the fixed IPv6 binding before changing account ownership';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_ipv6_account_guard BEFORE INSERT OR UPDATE ON accounts
FOR EACH ROW EXECUTE FUNCTION guard_openai_ipv6_account();

CREATE FUNCTION guard_openai_ipv6_proxy_pool() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE managed_proxy_id=NEW.proxy_id) THEN
        RAISE EXCEPTION 'Managed IPv6 proxies cannot join a rotating proxy pool';
    END IF;
    IF EXISTS(SELECT 1 FROM accounts a JOIN openai_ipv6_bindings b
        ON b.account_id=COALESCE(a.parent_account_id,a.id) WHERE a.id=NEW.account_id) THEN
        RAISE EXCEPTION 'Detach the fixed IPv6 binding before changing its proxy pool';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_ipv6_proxy_pool_guard BEFORE INSERT OR UPDATE ON account_proxies
FOR EACH ROW EXECUTE FUNCTION guard_openai_ipv6_proxy_pool();

CREATE FUNCTION guard_openai_ipv6_proxy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE managed_proxy_id=OLD.id) AND
       NOT (OLD.password='' AND EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE managed_proxy_id=OLD.id AND status IN ('provisioning','error'))
         AND NEW.protocol=OLD.protocol AND NEW.host=OLD.host AND NEW.port=OLD.port AND NEW.username=OLD.username
         AND NEW.status=OLD.status AND NEW.deleted_at IS NULL AND NEW.fallback_mode='none' AND NEW.backup_proxy_id IS NULL AND NEW.expires_at IS NULL) AND
       (NEW.protocol IS DISTINCT FROM OLD.protocol OR NEW.host IS DISTINCT FROM OLD.host
        OR NEW.port IS DISTINCT FROM OLD.port OR NEW.username IS DISTINCT FROM OLD.username
        OR NEW.password IS DISTINCT FROM OLD.password OR NEW.status IS DISTINCT FROM OLD.status
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at OR NEW.fallback_mode IS DISTINCT FROM OLD.fallback_mode
        OR NEW.backup_proxy_id IS DISTINCT FROM OLD.backup_proxy_id OR NEW.deleted_at IS NOT NULL) THEN
        RAISE EXCEPTION 'Detach the fixed IPv6 binding before changing its managed proxy';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_ipv6_proxy_guard BEFORE UPDATE ON proxies
FOR EACH ROW EXECUTE FUNCTION guard_openai_ipv6_proxy();

-- Managed credentials may not be reused as another proxy's backup route.
CREATE FUNCTION guard_openai_ipv6_backup_proxy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE managed_proxy_id=NEW.backup_proxy_id) THEN
        RAISE EXCEPTION 'Managed IPv6 proxies cannot be used as shared backup routes';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_ipv6_backup_proxy_guard BEFORE INSERT OR UPDATE ON proxies
FOR EACH ROW EXECUTE FUNCTION guard_openai_ipv6_backup_proxy();
