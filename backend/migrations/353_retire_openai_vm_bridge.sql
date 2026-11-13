-- Retire the VM transport without silently moving existing accounts to native
-- HTTP/WS or direct egress. Retain binding/configuration data for operator-led
-- recovery, and retain a compact audit of the state changed by this migration.
CREATE TABLE openai_vm_retirement_audit (
    account_id BIGINT PRIMARY KEY,
    parent_account_id BIGINT,
    previous_status TEXT NOT NULL,
    previous_schedulable BOOLEAN NOT NULL,
    managed_proxy_id BIGINT,
    retired_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

INSERT INTO openai_vm_retirement_audit
    (account_id,parent_account_id,previous_status,previous_schedulable,managed_proxy_id)
SELECT a.id,a.parent_account_id,a.status,a.schedulable,a.proxy_id
FROM accounts a LEFT JOIN accounts p ON p.id=a.parent_account_id
WHERE a.extra->>'openai_vm_bound'='true' OR a.extra->>'openai_vm_retired'='true'
   OR p.extra->>'openai_vm_bound'='true' OR p.extra->>'openai_vm_retired'='true'
   OR EXISTS(SELECT 1 FROM openai_vm_bindings b
       WHERE b.account_id=COALESCE(a.parent_account_id,a.id) OR b.managed_proxy_id=a.proxy_id);

UPDATE accounts a SET status='inactive',schedulable=FALSE,
    extra=COALESCE(a.extra,'{}'::jsonb)||'{"openai_vm_retired":true}'::jsonb ||
      CASE WHEN EXISTS(SELECT 1 FROM openai_vm_bindings b WHERE b.account_id=COALESCE(a.parent_account_id,a.id))
           THEN '{"openai_vm_bound":true}'::jsonb ELSE '{}'::jsonb END,
    updated_at=NOW()
WHERE a.id IN(SELECT account_id FROM openai_vm_retirement_audit);

INSERT INTO scheduler_outbox(event_type,account_id)
SELECT 'account_changed',account_id FROM openai_vm_retirement_audit;

-- Existing bindings remain protected by migration 308. This additional guard
-- covers marker-only accounts, stale imports, shadows, and proxy reuse.
CREATE FUNCTION guard_retired_openai_vm_account() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE owner_id BIGINT; bound BOOLEAN; parent_retired BOOLEAN; old_retired BOOLEAN;
BEGIN
    owner_id := COALESCE(NEW.parent_account_id,NEW.id);
    SELECT EXISTS(SELECT 1 FROM openai_vm_bindings b
        WHERE b.account_id=owner_id OR b.managed_proxy_id=NEW.proxy_id) INTO bound;
    SELECT EXISTS(SELECT 1 FROM accounts p WHERE p.id=NEW.parent_account_id AND
        (p.extra->>'openai_vm_bound'='true' OR p.extra->>'openai_vm_retired'='true')) INTO parent_retired;
    old_retired := FALSE;
    IF TG_OP='UPDATE' THEN
        old_retired := COALESCE(OLD.extra->>'openai_vm_bound'='true',FALSE)
            OR COALESCE(OLD.extra->>'openai_vm_retired'='true',FALSE);
        -- A previous version can detach the retained binding and restore its
        -- recorded route. Clearing retirement then requires a separate disabled
        -- account update after the VM binding and managed proxy are gone.
        IF old_retired AND NOT bound AND NOT parent_retired
           AND NEW.extra->>'openai_vm_bound' IS DISTINCT FROM 'true'
           AND NEW.extra->>'openai_vm_retired' IS DISTINCT FROM 'true'
           AND NEW.status <> 'active' AND NOT NEW.schedulable
           AND NOT EXISTS(SELECT 1 FROM openai_vm_retirement_audit r
               WHERE r.account_id=NEW.id AND r.managed_proxy_id IS NOT NULL
                 AND r.managed_proxy_id=NEW.proxy_id) THEN
            RETURN NEW;
        END IF;
    END IF;
    IF bound OR parent_retired OR old_retired
       OR NEW.extra->>'openai_vm_bound'='true' OR NEW.extra->>'openai_vm_retired'='true' THEN
        IF TG_OP='INSERT' THEN
            NEW.status := 'inactive';
            NEW.schedulable := FALSE;
            NEW.extra := COALESCE(NEW.extra,'{}'::jsonb)||'{"openai_vm_retired":true}'::jsonb;
        ELSIF NEW.status='active' OR NEW.schedulable
           OR NEW.extra->>'openai_vm_retired' IS DISTINCT FROM 'true' THEN
            RAISE EXCEPTION 'Retired VM routes must remain disabled until their original route is restored';
        END IF;
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_vm_retirement_guard BEFORE INSERT OR UPDATE ON accounts
FOR EACH ROW EXECUTE FUNCTION guard_retired_openai_vm_account();

CREATE FUNCTION guard_retired_openai_vm_proxy_reuse() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM openai_vm_bindings WHERE managed_proxy_id=NEW.proxy_id) THEN
        RAISE EXCEPTION 'Retired VM proxies cannot join a proxy pool';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_vm_retired_proxy_pool_guard BEFORE INSERT OR UPDATE ON account_proxies
FOR EACH ROW EXECUTE FUNCTION guard_retired_openai_vm_proxy_reuse();

CREATE FUNCTION guard_retired_openai_vm_backup_proxy() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
    IF EXISTS(SELECT 1 FROM openai_vm_bindings WHERE managed_proxy_id=NEW.backup_proxy_id) THEN
        RAISE EXCEPTION 'Retired VM proxies cannot be used as backup routes';
    END IF;
    RETURN NEW;
END $$;
CREATE TRIGGER openai_vm_retired_backup_guard BEFORE INSERT OR UPDATE ON proxies
FOR EACH ROW EXECUTE FUNCTION guard_retired_openai_vm_backup_proxy();
