-- Keep the exact pre-upgrade configuration for inspection. No credentials or
-- request content is stored here; this migration changes quality settings only.
CREATE TABLE quality_management_config_archive (
 kind TEXT NOT NULL, source_id BIGINT NOT NULL, config JSONB NOT NULL,
 archived_at TIMESTAMPTZ NOT NULL DEFAULT now(), PRIMARY KEY(kind,source_id)
);

-- Preserve customized scoring standards. Upgrade only the former defaults.
INSERT INTO quality_management_config_archive(kind,source_id,config)
 SELECT 'score',id,config FROM channel_quality_score_config
 WHERE config->'error_rate'->>'weight'='40' AND config->'cache_rate'->>'weight'='25'
 AND config->'ttft'->>'weight'='25' AND config->'duration'->>'weight'='10'
 AND COALESCE(config->'tps'->>'weight','0')='0';
UPDATE channel_quality_score_config SET config=jsonb_set(jsonb_set(jsonb_set(jsonb_set(config,
 '{error_rate,weight}','70'),'{cache_rate,weight}','10'),'{ttft,weight}','10'),'{duration,weight}','0')
 || jsonb_build_object('tps',COALESCE(NULLIF(config->'tps','null'::jsonb),'{"good":50,"bad":5}'::jsonb)||'{"weight":10}'::jsonb),
 version=version+1,updated_at=clock_timestamp()
 WHERE id IN(SELECT source_id FROM quality_management_config_archive WHERE kind='score');

-- The simple mode decides from the newest round. Keep old holds until fresh
-- checks complete; changing a setting alone is never recovery evidence.
INSERT INTO quality_management_config_archive(kind,source_id,config)
 SELECT 'active',id,protection FROM scheduled_test_plans
 WHERE protection->>'mode'='all_pass' AND EXISTS(
 SELECT 1 FROM jsonb_array_elements(protection->'rules') r WHERE r->'pass_rate'->>'enabled'='true');
UPDATE scheduled_test_plans p SET protection=jsonb_set(protection,'{rules}',(
 SELECT jsonb_agg(CASE WHEN r->'pass_rate'->>'enabled'='true' THEN jsonb_set(r,'{pass_rate}',
 r->'pass_rate'||'{"window_size":1,"recover_consecutive":1,"pause_below":100,"recover_at_least":100,"pause_on_single_failure":true}'::jsonb) ELSE r END ORDER BY n)
 FROM jsonb_array_elements(p.protection->'rules') WITH ORDINALITY AS rules(r,n)))
 WHERE id IN(SELECT source_id FROM quality_management_config_archive WHERE kind='active');

-- A group has one passive owner, even when its switch is off. Other groups on
-- a legacy policy keep their configuration, decision identity and evidence.
INSERT INTO quality_management_config_archive(kind,source_id,config)
 SELECT 'passive',p.id,p.config FROM passive_quality_policies p
 WHERE COALESCE(p.config->>'purpose','') NOT IN ('cache_target','group_quality_target') AND EXISTS(
 SELECT 1 FROM jsonb_array_elements_text(p.config->'group_ids') g JOIN passive_quality_policies target
 ON target.config->>'purpose' IN ('cache_target','group_quality_target')
 AND target.config->'group_ids' @> jsonb_build_array(g::bigint));
WITH scopes AS (
 SELECT p.id,COALESCE(jsonb_agg(DISTINCT g::bigint) FILTER(WHERE target.id IS NULL),'[]'::jsonb) kept,
 COALESCE(jsonb_agg(DISTINCT g::bigint) FILTER(WHERE target.id IS NOT NULL),'[]'::jsonb) retired
 FROM passive_quality_policies p CROSS JOIN LATERAL jsonb_array_elements_text(p.config->'group_ids') g
 LEFT JOIN passive_quality_policies target ON target.config->>'purpose' IN ('cache_target','group_quality_target')
 AND target.config->'group_ids' @> jsonb_build_array(g::bigint)
 WHERE p.id IN(SELECT source_id FROM quality_management_config_archive WHERE kind='passive') GROUP BY p.id
)
UPDATE passive_quality_policies p SET config=p.config||jsonb_build_object('group_ids',scopes.kept,
 'consolidated_group_ids',COALESCE(NULLIF(p.config->'consolidated_group_ids','null'::jsonb),'[]'::jsonb)||scopes.retired,
 'enabled',CASE WHEN scopes.kept='[]'::jsonb THEN false ELSE (p.config->>'enabled')::boolean END,
 'evaluation_version',COALESCE(NULLIF(p.config->>'evaluation_version','0')::bigint,p.version),
 'evidence_started_at',COALESCE(NULLIF(p.config->'evidence_started_at','null'::jsonb),to_jsonb(p.updated_at))),
 version=p.version+1,updated_at=clock_timestamp() FROM scopes WHERE p.id=scopes.id;

WITH removed AS (
 DELETE FROM passive_quality_states s USING passive_quality_policies p WHERE p.id=s.policy_id
 AND p.id IN(SELECT source_id FROM quality_management_config_archive WHERE kind='passive')
 AND NOT EXISTS(SELECT 1 FROM account_groups ag WHERE ag.account_id=s.account_id
 AND p.config->'group_ids' @> jsonb_build_array(ag.group_id)) RETURNING s.*
)
INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail)
 SELECT r.policy_id,r.account_id,p.config->>'name','group_target_consolidated',r.data
 FROM removed r JOIN passive_quality_policies p ON p.id=r.policy_id;

-- Remove only retired projections; preserve holds belonging to other owners.
WITH projections AS (
 SELECT a.id,COALESCE(jsonb_agg(h) FILTER(WHERE h IS NOT NULL AND
 (NOT EXISTS(SELECT 1 FROM quality_management_config_archive ar WHERE ar.kind='passive' AND ar.source_id=(h->>'policy_id')::bigint)
 OR EXISTS(SELECT 1 FROM passive_quality_states s WHERE s.account_id=a.id AND s.policy_id=(h->>'policy_id')::bigint))), '[]'::jsonb) holds
 FROM accounts a LEFT JOIN LATERAL jsonb_array_elements(CASE WHEN jsonb_typeof(a.extra->'passive_quality')='array' THEN a.extra->'passive_quality' ELSE '[]'::jsonb END) h ON true
 WHERE a.extra ? 'passive_quality' GROUP BY a.id
), changed AS (
 UPDATE accounts a SET extra=CASE WHEN p.holds='[]'::jsonb THEN a.extra-'passive_quality' ELSE jsonb_set(a.extra,'{passive_quality}',p.holds) END,
 updated_at=clock_timestamp() FROM projections p WHERE a.id=p.id AND a.extra->'passive_quality' IS DISTINCT FROM p.holds RETURNING a.id
)
INSERT INTO scheduler_outbox(event_type,account_id) SELECT 'account_changed',id FROM changed;
