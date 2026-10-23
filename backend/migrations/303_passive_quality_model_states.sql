-- V2 model-scoped protection owns independent lifecycle and trial capacity per
-- requested model. Legacy/account-scoped policies retain the empty identity.
ALTER TABLE passive_quality_states ADD COLUMN model_key TEXT NOT NULL DEFAULT '';
ALTER TABLE passive_quality_leases ADD COLUMN model_key TEXT NOT NULL DEFAULT '';
UPDATE passive_quality_states s SET model_key=COALESCE(NULLIF(s.data->>'model',''),p.config->>'model','')
FROM passive_quality_policies p WHERE p.id=s.policy_id
AND p.config->>'schema_version'='2' AND p.config->>'scope'='model';
UPDATE passive_quality_leases l SET model_key=s.model_key
FROM passive_quality_states s WHERE s.policy_id=l.policy_id AND s.account_id=l.account_id;
ALTER TABLE passive_quality_leases DROP CONSTRAINT passive_quality_leases_policy_id_account_id_fkey;
ALTER TABLE passive_quality_states DROP CONSTRAINT passive_quality_states_pkey;
ALTER TABLE passive_quality_states ADD PRIMARY KEY(policy_id,account_id,model_key);
ALTER TABLE passive_quality_leases DROP CONSTRAINT passive_quality_leases_pkey;
ALTER TABLE passive_quality_leases ADD PRIMARY KEY(id,policy_id,model_key);
ALTER TABLE passive_quality_leases ADD CONSTRAINT passive_quality_leases_state_fkey
FOREIGN KEY(policy_id,account_id,model_key) REFERENCES passive_quality_states(policy_id,account_id,model_key) ON DELETE CASCADE;
DROP INDEX passive_quality_leases_state;
CREATE INDEX passive_quality_leases_state ON passive_quality_leases(policy_id,account_id,model_key,generation);
