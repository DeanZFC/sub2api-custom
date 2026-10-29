-- Replace bounded customer-traffic trials with dedicated active recovery.
-- Keep existing thresholds and all manual/intelligence restrictions.
LOCK TABLE passive_quality_policies IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE passive_quality_states IN SHARE ROW EXCLUSIVE MODE;

INSERT INTO quality_management_config_archive(kind,source_id,config)
 SELECT 'passive_before_active_recovery',id,config FROM passive_quality_policies
 WHERE config->>'purpose' IN ('group_quality_target','cache_target');

UPDATE passive_quality_policies SET
 config=config||jsonb_build_object(
  'recovery_mode','active','traffic_mode','strict','window_seconds',3600,'max_samples',0,
  'min_samples',20,'new_samples',5,'confirmations',2,'recovery_samples',5,
  'cooldown_seconds',300,'max_cooldown_seconds',3600,
  'trial_requests',20,'trial_seconds',900,'trial_concurrency',1,
  'stability',jsonb_build_object('availability_pause_seconds',60,'performance_pause_seconds',300,'recovery_seconds',120,'new_session_percent',100),
  'evidence_started_at',COALESCE(NULLIF(config->'evidence_started_at','null'::jsonb),to_jsonb(updated_at)),
  'evaluation_version',version+1),
 version=version+1,updated_at=clock_timestamp()
 WHERE config->>'purpose' IN ('group_quality_target','cache_target');

UPDATE passive_quality_states s SET data=(s.data-'active_recovery_config'-'active_confirmations')||
 jsonb_build_object('version',p.version,'generation',COALESCE((s.data->>'generation')::bigint,0)+1,
  'phase',CASE WHEN s.data->>'phase' IN ('trial','probing') THEN 'paused' ELSE s.data->>'phase' END,
  'reason',CASE WHEN s.data->>'phase' IN ('trial','probing') THEN 'active_recovery_pending' ELSE s.data->>'reason' END,
  'next_at',CASE WHEN s.data->>'phase' IN ('trial','probing') THEN to_jsonb(clock_timestamp()) ELSE s.data->'next_at' END,
  'last_evidence_at',clock_timestamp(),'updated_at',clock_timestamp(),
  'hits',0,'recovery_hits',0,'used',0,'inflight',0,'cache_allocation_reduced',false,
  'confirmation_metric','','suspicion_since','0001-01-01T00:00:00Z','recovery_since','0001-01-01T00:00:00Z')
 FROM passive_quality_policies p WHERE p.id=s.policy_id AND p.config->>'recovery_mode'='active';

DELETE FROM passive_quality_leases l USING passive_quality_policies p
 WHERE p.id=l.policy_id AND p.config->>'recovery_mode'='active';

-- Replace only these policies' routing projections. No existing hard pause is
-- released, and stale per-account cache soft limits are not left behind.
WITH affected AS (SELECT DISTINCT account_id FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE p.config->>'recovery_mode'='active'),
 projections AS (
 SELECT a.id,COALESCE((SELECT jsonb_agg(h) FROM (
  SELECT h FROM jsonb_array_elements(CASE WHEN jsonb_typeof(a.extra->'passive_quality')='array' THEN a.extra->'passive_quality' ELSE '[]'::jsonb END) h
  WHERE NOT EXISTS(SELECT 1 FROM passive_quality_policies p WHERE p.config->>'recovery_mode'='active' AND p.id::text=h->>'policy_id')
  UNION ALL
  SELECT jsonb_build_object('policy_id',p.id,'policy_name',p.config->>'name','scope','model','model',s.model_key,
   'phase',s.data->>'phase','reason',s.data->>'reason','recovery_mode','active')
  FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
  WHERE s.account_id=a.id AND p.config->>'recovery_mode'='active' AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce'
  AND s.data->>'phase' IN ('paused','probing','trial')
 ) holds),'[]'::jsonb) AS holds FROM accounts a JOIN affected ON affected.account_id=a.id
), changed AS (
 UPDATE accounts a SET extra=CASE WHEN p.holds='[]'::jsonb THEN COALESCE(a.extra,'{}')-'passive_quality' ELSE jsonb_set(COALESCE(a.extra,'{}'),'{passive_quality}',p.holds) END,updated_at=clock_timestamp()
 FROM projections p WHERE a.id=p.id RETURNING a.id
) INSERT INTO scheduler_outbox(event_type,account_id) SELECT 'account_changed',id FROM changed;

-- Index the whole time window instead of capping its request count.
CREATE INDEX IF NOT EXISTS idx_usage_logs_quality_full_hour
 ON usage_logs(account_id,group_id,(COALESCE(NULLIF(TRIM(requested_model),''),model)),created_at DESC)
 WHERE duration_ms IS NOT NULL AND COALESCE(request_type,0) NOT IN (4,6);
