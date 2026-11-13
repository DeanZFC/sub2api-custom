-- Independent provider; never changes existing legacy IPv6 bindings or routes.
-- Protocol imported from 0.2.21-yoshub.29, integrated with account families/pools.
SET LOCAL lock_timeout = '5s';
CREATE TABLE ovh_ipv6_settings (
 id SMALLINT PRIMARY KEY CHECK(id=1), enabled BOOLEAN NOT NULL DEFAULT FALSE,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
INSERT INTO ovh_ipv6_settings(id) VALUES(1);
CREATE TABLE ovh_ipv6_leases (
 proxy_id BIGINT PRIMARY KEY, lease_id TEXT NOT NULL UNIQUE,
 address INET NOT NULL UNIQUE CHECK(family(address)=6), control_url TEXT NOT NULL,
 owner_account_id BIGINT, owner_moderation_key TEXT,
 state TEXT NOT NULL CHECK(state IN ('reserved','bound','retiring')),
 confirmed BOOLEAN NOT NULL DEFAULT FALSE, next_attempt_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 last_error TEXT NOT NULL DEFAULT '', created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 CHECK(owner_account_id IS NULL OR owner_moderation_key IS NULL),
 CHECK(owner_moderation_key IS NULL OR owner_moderation_key ~ '^[0-9a-f]{64}$')
);
CREATE INDEX idx_ovh_ipv6_pending ON ovh_ipv6_leases(next_attempt_at) WHERE NOT confirmed OR state='retiring';
CREATE UNIQUE INDEX idx_ovh_ipv6_account_owner ON ovh_ipv6_leases(owner_account_id) WHERE state='bound' AND owner_account_id IS NOT NULL;
CREATE UNIQUE INDEX idx_ovh_ipv6_moderation_owner ON ovh_ipv6_leases(owner_moderation_key) WHERE state='bound' AND owner_moderation_key IS NOT NULL;
CREATE UNIQUE INDEX idx_ovh_ipv6_proxy_identity ON proxies(username) WHERE username LIKE 'ovh6\_%' ESCAPE '\';

CREATE FUNCTION ovh_ipv6_claim() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE lease ovh_ipv6_leases%ROWTYPE; previous ovh_ipv6_leases%ROWTYPE; owner BIGINT; route_changed BOOLEAN;
BEGIN
 owner:=COALESCE(NEW.parent_account_id,NEW.id);
 route_changed:=TG_OP='UPDATE' AND (NEW.proxy_id IS DISTINCT FROM OLD.proxy_id OR NEW.deleted_at IS DISTINCT FROM OLD.deleted_at OR NEW.parent_account_id IS DISTINCT FROM OLD.parent_account_id);
 IF TG_OP='UPDATE' THEN
  SELECT * INTO previous FROM ovh_ipv6_leases WHERE proxy_id=OLD.proxy_id;
  IF previous.proxy_id IS NOT NULL AND route_changed THEN
   IF OLD.status NOT IN ('disabled','inactive') OR NEW.status NOT IN ('disabled','inactive') OR
    EXISTS(SELECT 1 FROM accounts WHERE (id=COALESCE(OLD.parent_account_id,OLD.id) OR parent_account_id=COALESCE(OLD.parent_account_id,OLD.id)) AND deleted_at IS NULL AND status NOT IN ('disabled','inactive')) THEN
    RAISE EXCEPTION 'Disable the whole account family and drain requests before changing fixed IPv6';
   END IF;
   IF NEW.parent_account_id IS DISTINCT FROM OLD.parent_account_id THEN RAISE EXCEPTION 'Managed IPv6 account ownership cannot change'; END IF;
   -- Release the uniqueness slot in the same transaction before claiming a replacement.
   IF OLD.parent_account_id IS NULL THEN
    UPDATE ovh_ipv6_leases SET state='retiring',confirmed=FALSE,next_attempt_at=NOW() WHERE proxy_id=OLD.proxy_id AND owner_account_id=OLD.id;
   END IF;
  END IF;
 END IF;
 -- Markers are derived from leases, never trusted from API JSON or stale snapshots.
 IF jsonb_typeof(NEW.extra)='object' THEN NEW.extra:=NEW.extra-'ovh_ipv6_bound'; END IF;
 IF NEW.deleted_at IS NOT NULL OR NEW.proxy_id IS NULL THEN RETURN NEW; END IF;
 SELECT * INTO lease FROM ovh_ipv6_leases WHERE proxy_id=NEW.proxy_id FOR UPDATE;
 IF NOT FOUND THEN
  IF EXISTS(SELECT 1 FROM proxies WHERE id=NEW.proxy_id AND username LIKE 'ovh6\_%' ESCAPE '\') THEN RAISE EXCEPTION 'Managed IPv6 credentials require an authoritative lease'; END IF;
  RETURN NEW;
 END IF;
 IF NEW.platform<>'openai' OR NEW.type<>'oauth' OR NEW.extra->>'openai_excel_bps'='true' OR
  lower(trim(NEW.credentials->>'auth_mode'))='agentidentity' OR NEW.credentials->>'client_id'='app_fnr0pYvVwwFDocDumLG3H2Bp' OR
  NEW.extra->>'openai_vm_bound'='true' OR NEW.extra->>'openai_vm_retired'='true' OR
  EXISTS(SELECT 1 FROM openai_ipv6_bindings WHERE account_id=owner) THEN
  RAISE EXCEPTION 'OVH IPv6 requires a native OpenAI OAuth family without another managed route or Excel BPS';
 END IF;
 IF lease.owner_moderation_key IS NOT NULL OR lease.state NOT IN ('reserved','bound') OR
   (lease.owner_account_id IS NOT NULL AND lease.owner_account_id<>owner) OR
   NOT EXISTS(SELECT 1 FROM proxies WHERE id=NEW.proxy_id AND deleted_at IS NULL AND status='active') THEN
  RAISE EXCEPTION 'OVH IPv6 is owned by another account/key or permanently retired';
 END IF;
 IF TG_OP='UPDATE' AND route_changed AND (OLD.status NOT IN ('disabled','inactive') OR NEW.status NOT IN ('disabled','inactive') OR
  EXISTS(SELECT 1 FROM accounts WHERE parent_account_id=owner AND deleted_at IS NULL AND status NOT IN ('disabled','inactive'))) THEN
  RAISE EXCEPTION 'Disable the whole account family before binding fixed IPv6';
 END IF;
 IF NEW.parent_account_id IS NOT NULL AND (lease.owner_account_id IS NULL OR NOT EXISTS(SELECT 1 FROM accounts WHERE id=owner AND proxy_id=NEW.proxy_id AND deleted_at IS NULL)) THEN
  RAISE EXCEPTION 'OVH IPv6 must first be assigned to the parent account';
 END IF;
 IF NEW.proxy_fallback_origin_id IS NOT NULL THEN RAISE EXCEPTION 'Managed IPv6 cannot use fallback routes'; END IF;
 UPDATE ovh_ipv6_leases SET owner_account_id=owner,state='bound' WHERE proxy_id=NEW.proxy_id;
 IF jsonb_typeof(NEW.extra) IS DISTINCT FROM 'object' THEN NEW.extra:='{}'::jsonb; END IF;
 NEW.extra:=NEW.extra||'{"ovh_ipv6_bound":true}'::jsonb;
 RETURN NEW;
END $$;
-- Alphabetically after legacy shadow inheritance trigger.
CREATE TRIGGER ovh_ipv6_claim BEFORE INSERT OR UPDATE ON accounts FOR EACH ROW EXECUTE FUNCTION ovh_ipv6_claim();

-- Hard deletes must obey the same family drain precondition as detaching or
-- soft-deleting. This also protects cascades and direct repository delete calls.
CREATE FUNCTION ovh_ipv6_delete_guard() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM ovh_ipv6_leases WHERE proxy_id=OLD.proxy_id) AND
  (OLD.status NOT IN ('disabled','inactive') OR EXISTS(
   SELECT 1 FROM accounts WHERE (id=COALESCE(OLD.parent_account_id,OLD.id) OR parent_account_id=COALESCE(OLD.parent_account_id,OLD.id))
   AND deleted_at IS NULL AND status NOT IN ('disabled','inactive'))) THEN
  RAISE EXCEPTION 'Disable the whole account family and drain requests before deleting fixed IPv6';
 END IF;
 RETURN OLD;
END $$;
CREATE TRIGGER ovh_ipv6_delete_guard BEFORE DELETE ON accounts FOR EACH ROW EXECUTE FUNCTION ovh_ipv6_delete_guard();

CREATE FUNCTION ovh_ipv6_retire() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF OLD.proxy_id IS NULL OR OLD.parent_account_id IS NOT NULL THEN RETURN NULL; END IF;
 IF TG_OP='UPDATE' AND NEW.proxy_id IS NOT DISTINCT FROM OLD.proxy_id AND NEW.deleted_at IS NULL THEN RETURN NULL; END IF;
 UPDATE ovh_ipv6_leases SET state='retiring',confirmed=FALSE,next_attempt_at=NOW()
  WHERE proxy_id=OLD.proxy_id AND owner_account_id=OLD.id;
 IF FOUND THEN
  IF TG_OP='UPDATE' AND NEW.deleted_at IS NULL THEN
   UPDATE accounts SET proxy_id=NEW.proxy_id,updated_at=NOW() WHERE parent_account_id=OLD.id AND proxy_id=OLD.proxy_id AND deleted_at IS NULL;
  END IF;
  UPDATE proxies SET status='inactive',deleted_at=NOW(),updated_at=NOW() WHERE id=OLD.proxy_id;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER ovh_ipv6_retire AFTER UPDATE OR DELETE ON accounts FOR EACH ROW EXECUTE FUNCTION ovh_ipv6_retire();

CREATE FUNCTION ovh_ipv6_pool_guard() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM ovh_ipv6_leases WHERE proxy_id=NEW.proxy_id) OR
 EXISTS(SELECT 1 FROM accounts a JOIN ovh_ipv6_leases l ON l.proxy_id=a.proxy_id WHERE a.id=NEW.account_id) THEN
  RAISE EXCEPTION 'Managed IPv6 cannot join a rotating proxy pool';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ovh_ipv6_pool_guard BEFORE INSERT OR UPDATE ON account_proxies FOR EACH ROW EXECUTE FUNCTION ovh_ipv6_pool_guard();
-- Pool replacement runs after account UPDATE; inspect final state at commit.
CREATE FUNCTION ovh_ipv6_final_pool_guard() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM accounts a JOIN ovh_ipv6_leases l ON l.proxy_id=a.proxy_id JOIN account_proxies p ON p.account_id=a.id WHERE a.id=NEW.id AND a.deleted_at IS NULL) THEN
  RAISE EXCEPTION 'Clear the rotating proxy pool before binding fixed IPv6';
 END IF;
 RETURN NULL;
END $$;
CREATE CONSTRAINT TRIGGER ovh_ipv6_final_pool_guard AFTER INSERT OR UPDATE ON accounts DEFERRABLE INITIALLY DEFERRED FOR EACH ROW WHEN (NEW.extra->>'ovh_ipv6_bound'='true') EXECUTE FUNCTION ovh_ipv6_final_pool_guard();

CREATE FUNCTION ovh_ipv6_proxy_guard() RETURNS TRIGGER LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM ovh_ipv6_leases WHERE proxy_id=NEW.backup_proxy_id) THEN RAISE EXCEPTION 'Managed IPv6 cannot be shared as a backup proxy'; END IF;
 IF TG_OP='UPDATE' AND EXISTS(SELECT 1 FROM ovh_ipv6_leases WHERE proxy_id=OLD.id) AND
  (NEW.protocol,NEW.host,NEW.port,NEW.username,NEW.password,NEW.fallback_mode,NEW.backup_proxy_id,NEW.expires_at)
  IS DISTINCT FROM (OLD.protocol,OLD.host,OLD.port,OLD.username,OLD.password,OLD.fallback_mode,OLD.backup_proxy_id,OLD.expires_at) THEN
  RAISE EXCEPTION 'Managed IPv6 routing is immutable';
 END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ovh_ipv6_proxy_guard BEFORE INSERT OR UPDATE ON proxies FOR EACH ROW EXECUTE FUNCTION ovh_ipv6_proxy_guard();
CREATE FUNCTION ovh_ipv6_proxy_retire() RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE lease ovh_ipv6_leases%ROWTYPE;
BEGIN
 IF TG_OP='UPDATE' AND (NEW.deleted_at IS NULL OR OLD.deleted_at IS NOT NULL) THEN RETURN NEW; END IF;
 SELECT * INTO lease FROM ovh_ipv6_leases WHERE proxy_id=OLD.id FOR UPDATE;
 IF FOUND THEN
  IF lease.state='bound' AND (lease.owner_moderation_key IS NOT NULL OR EXISTS(SELECT 1 FROM accounts WHERE proxy_id=OLD.id AND deleted_at IS NULL)) THEN
   RAISE EXCEPTION 'Managed IPv6 is still owned; detach the owner first';
  END IF;
  UPDATE ovh_ipv6_leases SET state='retiring',confirmed=FALSE,next_attempt_at=NOW() WHERE proxy_id=OLD.id AND state<>'retiring';
 END IF;
 IF TG_OP='DELETE' THEN RETURN OLD; END IF;
 RETURN NEW;
END $$;
CREATE TRIGGER ovh_ipv6_proxy_retire BEFORE UPDATE OF deleted_at OR DELETE ON proxies FOR EACH ROW EXECUTE FUNCTION ovh_ipv6_proxy_retire();
