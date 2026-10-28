-- Custom passive rules are retired. Preserve their configuration only as an
-- audit archive; group targets (including the earlier cache_target purpose)
-- and their evidence, leases and account projections remain unchanged.
LOCK TABLE passive_quality_policies IN ACCESS EXCLUSIVE MODE;

INSERT INTO quality_management_config_archive(kind,source_id,config)
 SELECT 'passive_retired',id,config FROM passive_quality_policies
 WHERE COALESCE(config->>'purpose','') NOT IN ('cache_target','group_quality_target');

INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail)
 SELECT s.policy_id,s.account_id,COALESCE(p.config->>'name',''),
 'custom_rule_retired',s.data||'{"reason":"custom_rule_retired"}'::jsonb
 FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
 WHERE COALESCE(p.config->>'purpose','') NOT IN ('cache_target','group_quality_target');

-- Delete dependent states and trial leases through their existing foreign keys.
DELETE FROM passive_quality_policies
 WHERE COALESCE(config->>'purpose','') NOT IN ('cache_target','group_quality_target');

-- Only remove projections owned by retired rules. Manual/active-test holds,
-- account status, schedulability and remaining group-target holds are untouched.
WITH projections AS (
 SELECT a.id,COALESCE((SELECT jsonb_agg(h ORDER BY ordinal)
 FROM jsonb_array_elements(a.extra->'passive_quality') WITH ORDINALITY AS holds(h,ordinal)
 WHERE NOT EXISTS(SELECT 1 FROM quality_management_config_archive ar
 WHERE ar.kind='passive_retired' AND ar.source_id::text=h->>'policy_id')),'[]'::jsonb) holds
 FROM accounts a WHERE jsonb_typeof(a.extra->'passive_quality')='array'
), changed AS (
 UPDATE accounts a SET extra=CASE WHEN p.holds='[]'::jsonb THEN a.extra-'passive_quality'
 ELSE jsonb_set(a.extra,'{passive_quality}',p.holds) END,updated_at=clock_timestamp()
 FROM projections p WHERE a.id=p.id AND a.extra->'passive_quality' IS DISTINCT FROM p.holds
 RETURNING a.id
)
INSERT INTO scheduler_outbox(event_type,account_id) SELECT 'account_changed',id FROM changed;

-- Reject recreation by old clients or an older instance during an upgrade.
ALTER TABLE passive_quality_policies ADD CONSTRAINT passive_quality_group_targets_only
 CHECK (COALESCE(config->>'purpose','') IN ('cache_target','group_quality_target'));
