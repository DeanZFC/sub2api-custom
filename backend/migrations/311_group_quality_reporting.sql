-- Additive: old writers continue maintaining the rolling aggregates via triggers.
CREATE TABLE group_quality_report_snapshots (
 policy_id BIGINT PRIMARY KEY REFERENCES passive_quality_policies(id) ON DELETE CASCADE,
 policy_version BIGINT NOT NULL, measured_at TIMESTAMPTZ NOT NULL, data JSONB NOT NULL
);
CREATE TABLE group_quality_budget_minutes (
 policy_id BIGINT NOT NULL REFERENCES passive_quality_policies(id) ON DELETE CASCADE,
 minute TIMESTAMPTZ NOT NULL, charged_tokens BIGINT NOT NULL DEFAULT 0,
 PRIMARY KEY(policy_id,minute)
);
CREATE FUNCTION group_quality_lease_charge(l group_quality_traffic_leases) RETURNS BIGINT
LANGUAGE SQL IMMUTABLE AS $$
 SELECT CASE WHEN l.validation AND NOT l.unused THEN
 CASE WHEN l.cache_reported AND l.input_tokens>0 THEN l.input_tokens ELSE GREATEST(l.reserved_input,COALESCE(l.input_tokens,0)) END ELSE 0 END
$$;
CREATE FUNCTION group_quality_budget_update() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE delta BIGINT;
BEGIN
 IF TG_OP='UPDATE' AND NEW.policy_id=OLD.policy_id AND date_trunc('minute',NEW.created_at)=date_trunc('minute',OLD.created_at) THEN
   delta := group_quality_lease_charge(NEW)-group_quality_lease_charge(OLD);
   IF delta<>0 THEN
     INSERT INTO group_quality_budget_minutes VALUES(NEW.policy_id,date_trunc('minute',NEW.created_at),delta)
     ON CONFLICT(policy_id,minute) DO UPDATE SET charged_tokens=group_quality_budget_minutes.charged_tokens+excluded.charged_tokens;
   END IF;
 ELSE
   IF TG_OP<>'INSERT' AND group_quality_lease_charge(OLD)<>0 THEN
     UPDATE group_quality_budget_minutes SET charged_tokens=charged_tokens-group_quality_lease_charge(OLD)
     WHERE policy_id=OLD.policy_id AND minute=date_trunc('minute',OLD.created_at);
   END IF;
   IF TG_OP<>'DELETE' AND group_quality_lease_charge(NEW)<>0 THEN
     INSERT INTO group_quality_budget_minutes VALUES(NEW.policy_id,date_trunc('minute',NEW.created_at),group_quality_lease_charge(NEW))
     ON CONFLICT(policy_id,minute) DO UPDATE SET charged_tokens=group_quality_budget_minutes.charged_tokens+excluded.charged_tokens;
   END IF;
 END IF;
 RETURN NULL;
END $$;
CREATE TRIGGER group_quality_budget_update AFTER INSERT OR UPDATE OR DELETE ON group_quality_traffic_leases
FOR EACH ROW EXECUTE FUNCTION group_quality_budget_update();
INSERT INTO group_quality_budget_minutes
SELECT policy_id,date_trunc('minute',created_at),sum(group_quality_lease_charge(l))
FROM group_quality_traffic_leases l GROUP BY policy_id,date_trunc('minute',created_at);
CREATE INDEX group_quality_traffic_attempt ON group_quality_traffic_leases(group_id,account_id,request_key,model,created_at DESC) WHERE NOT unused;
