-- Optional OpenAI VM adapter. No credentials are copied out of accounts.
CREATE TABLE openai_vm_config (
    id SMALLINT PRIMARY KEY CHECK (id = 1),
    enabled BOOLEAN NOT NULL DEFAULT FALSE,
    base_url TEXT NOT NULL DEFAULT '',
    proxy_url TEXT NOT NULL DEFAULT '',
    encrypted_secret TEXT NOT NULL DEFAULT '',
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO openai_vm_config(id) VALUES(1);
CREATE TABLE openai_vm_bindings (
    account_id BIGINT PRIMARY KEY REFERENCES accounts(id) ON DELETE RESTRICT,
    managed_proxy_id BIGINT NOT NULL UNIQUE REFERENCES proxies(id) ON DELETE RESTRICT,
    original_proxy_id BIGINT,
    original_proxy_ids JSONB NOT NULL DEFAULT '[]',
    original_fallback_id BIGINT,
    runtime_type TEXT NOT NULL CHECK(runtime_type IN ('docker','kvm')),
    slot_id TEXT NOT NULL DEFAULT '',
    status TEXT NOT NULL DEFAULT 'provisioning',
    last_error TEXT NOT NULL DEFAULT '',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

-- Protect the route in the database as imports, batch edits and expiry sweeps
-- also write these tables. Unbinding deletes the binding in the same transaction
-- that restores the original route. Ordinary metadata/credential edits still work.
CREATE FUNCTION guard_openai_vm_account() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE managed BIGINT; owner_id BIGINT; parent_proxy BIGINT; parent_fallback BIGINT;
BEGIN
    IF TG_OP='INSERT' AND NEW.parent_account_id IS NOT NULL THEN
        -- Serialize shadow creation with lifecycle changes on its parent. Read
        -- the current route after the lock, never a caller's stale snapshot.
        SELECT proxy_id,proxy_fallback_origin_id INTO parent_proxy,parent_fallback
        FROM accounts WHERE id=NEW.parent_account_id FOR UPDATE;
        NEW.proxy_id := parent_proxy;
        NEW.proxy_fallback_origin_id := parent_fallback;
        NEW.extra := COALESCE(NEW.extra,'{}'::jsonb)-'openai_vm_bound';
    END IF;
    owner_id := COALESCE(NEW.parent_account_id, NEW.id);
    SELECT managed_proxy_id INTO managed FROM openai_vm_bindings WHERE account_id=owner_id;
    IF managed IS NOT NULL THEN
        IF TG_OP='INSERT' THEN
            NEW.proxy_id := managed;
            NEW.extra := COALESCE(NEW.extra,'{}'::jsonb) || '{"openai_vm_bound":true}'::jsonb;
        ELSIF NEW.proxy_id IS DISTINCT FROM managed
            OR NEW.deleted_at IS NOT NULL OR NEW.platform <> 'openai' OR NEW.type <> 'oauth'
            OR NEW.parent_account_id IS DISTINCT FROM OLD.parent_account_id
            OR NEW.proxy_fallback_origin_id IS NOT NULL
            OR NEW.extra->>'openai_vm_bound' IS DISTINCT FROM 'true' THEN
            RAISE EXCEPTION 'Detach the OpenAI VM slot before changing or deleting its account route';
        END IF;
    ELSIF TG_OP='UPDATE' AND OLD.parent_account_id IS DISTINCT FROM NEW.parent_account_id
        AND EXISTS(SELECT 1 FROM openai_vm_bindings WHERE account_id=COALESCE(OLD.parent_account_id,OLD.id)) THEN
        RAISE EXCEPTION 'Detach the OpenAI VM slot before changing account ownership';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_vm_account_guard BEFORE INSERT OR UPDATE ON accounts
FOR EACH ROW EXECUTE FUNCTION guard_openai_vm_account();

CREATE FUNCTION guard_openai_vm_proxy_pool() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM accounts a JOIN openai_vm_bindings b
        ON b.account_id=COALESCE(a.parent_account_id,a.id) WHERE a.id=NEW.account_id) THEN
        RAISE EXCEPTION 'Detach the OpenAI VM slot before changing its proxy pool';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_vm_proxy_pool_guard BEFORE INSERT OR UPDATE ON account_proxies
FOR EACH ROW EXECUTE FUNCTION guard_openai_vm_proxy_pool();

CREATE FUNCTION guard_openai_vm_proxy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM openai_vm_bindings WHERE managed_proxy_id=OLD.id) AND
       (NEW.protocol IS DISTINCT FROM OLD.protocol OR NEW.host IS DISTINCT FROM OLD.host
        OR NEW.port IS DISTINCT FROM OLD.port OR NEW.username IS DISTINCT FROM OLD.username
        OR NEW.password IS DISTINCT FROM OLD.password OR NEW.status IS DISTINCT FROM OLD.status
        OR NEW.expires_at IS DISTINCT FROM OLD.expires_at OR NEW.fallback_mode IS DISTINCT FROM OLD.fallback_mode
        OR NEW.backup_proxy_id IS DISTINCT FROM OLD.backup_proxy_id OR NEW.deleted_at IS NOT NULL) THEN
        RAISE EXCEPTION 'Detach the OpenAI VM slot before changing its managed proxy';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_vm_proxy_guard BEFORE UPDATE ON proxies
FOR EACH ROW EXECUTE FUNCTION guard_openai_vm_proxy();
