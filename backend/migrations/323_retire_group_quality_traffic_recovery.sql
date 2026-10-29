-- Group objectives recover only through dedicated active probes. Archive the
-- complete previous policy/state/lease representation before fencing old work.
-- Deploy all application instances together; reverting requires this archive
-- and a pre-upgrade database backup, not merely an older application binary.
LOCK TABLE passive_quality_policies IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE passive_quality_states IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE passive_quality_leases IN SHARE ROW EXCLUSIVE MODE;
LOCK TABLE group_quality_traffic_leases IN SHARE ROW EXCLUSIVE MODE;

INSERT INTO quality_management_config_archive(kind,source_id,config)
SELECT 'retired_group_traffic_recovery',p.id,jsonb_build_object(
 'policy',to_jsonb(p),
 'states',COALESCE((SELECT jsonb_agg(to_jsonb(s) ORDER BY s.account_id,s.model_key) FROM passive_quality_states s WHERE s.policy_id=p.id),'[]'::jsonb),
 'passive_leases',COALESCE((SELECT jsonb_agg(to_jsonb(l) ORDER BY l.id,l.model_key) FROM passive_quality_leases l WHERE l.policy_id=p.id),'[]'::jsonb),
 'traffic_leases',COALESCE((SELECT jsonb_agg(to_jsonb(l) ORDER BY l.lease_id) FROM group_quality_traffic_leases l WHERE l.policy_id=p.id),'[]'::jsonb))
FROM passive_quality_policies p WHERE p.config->>'purpose' IN ('group_quality_target','cache_target')
ON CONFLICT(kind,source_id) DO NOTHING;

-- Two confirmation batches must fit within the ten-minute active probe budget.
-- Preserve valid custom durations; archive and reset only impossible old spans.
UPDATE passive_quality_policies SET
 config=(config-'trial_requests'-'trial_seconds'-'trial_concurrency')||jsonb_build_object(
  'recovery_mode','active','traffic_mode','strict','window_seconds',3600,'max_samples',0,
  'action','pause','evaluation_version',version+1,'confirmations',2,'recovery_samples',5,
  'stability',CASE WHEN config->'stability' IS NULL OR config->'stability'='null'::jsonb THEN 'null'::jsonb ELSE ((config->'stability')-'new_session_percent')||CASE WHEN COALESCE((config->'stability'->>'recovery_seconds')::int,0)>=540 THEN jsonb_build_object('recovery_seconds',120) ELSE '{}'::jsonb END END,
  'evidence_started_at',COALESCE(NULLIF(config->'evidence_started_at','null'::jsonb),to_jsonb(updated_at))),
 version=version+1,updated_at=clock_timestamp()
WHERE config->>'purpose' IN ('group_quality_target','cache_target');

INSERT INTO passive_quality_events(policy_id,account_id,policy_name,kind,detail)
SELECT s.policy_id,s.account_id,COALESCE(p.config->>'name',''),'customer_recovery_retired',s.data
FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
WHERE p.config->>'purpose' IN ('group_quality_target','cache_target');

UPDATE passive_quality_states s SET data=(s.data-'active_recovery_config'-'active_confirmations'
 -'trial_started_at'-'trial_ends_at'-'last_trial_admission_at'-'last_trial_observed_at')||jsonb_build_object(
 'version',p.version,'generation',COALESCE((s.data->>'generation')::bigint,0)+1,
 'phase',CASE WHEN s.data->>'phase' IN ('trial','probing','limited') OR s.data->>'cache_allocation_reduced'='true' THEN 'paused' ELSE s.data->>'phase' END,
 'reason',CASE WHEN s.data->>'phase' IN ('trial','probing','limited') OR s.data->>'cache_allocation_reduced'='true' THEN 'active_recovery_pending: customer_recovery_retired' ELSE s.data->>'reason' END,
 'next_at',CASE WHEN s.data->>'phase' IN ('trial','probing','limited') OR s.data->>'cache_allocation_reduced'='true' THEN to_jsonb(clock_timestamp()) ELSE s.data->'next_at' END,
 'updated_at',clock_timestamp(),'hits',0,'recovery_hits',0,'used',0,'inflight',0,'cache_allocation_reduced',false,
 'confirmation_metric','','suspicion_since','0001-01-01T00:00:00Z','recovery_since','0001-01-01T00:00:00Z')
FROM passive_quality_policies p WHERE p.id=s.policy_id AND p.config->>'purpose' IN ('group_quality_target','cache_target');

DELETE FROM passive_quality_leases l USING passive_quality_policies p
WHERE p.id=l.policy_id AND p.config->>'purpose' IN ('group_quality_target','cache_target');

-- Keep the original rows and counters for history; close the authority only.
UPDATE group_quality_traffic_leases l SET released_at=COALESCE(l.released_at,clock_timestamp()),
 fallback_expires_at=CASE WHEN l.fallback THEN LEAST(l.fallback_expires_at,clock_timestamp()) ELSE l.fallback_expires_at END
FROM passive_quality_policies p WHERE p.id=l.policy_id AND p.config->>'purpose' IN ('group_quality_target','cache_target');

-- Fence stale instances and in-flight legacy callbacks across the deployment.
CREATE FUNCTION reject_group_target_customer_trial() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM passive_quality_policies WHERE id=NEW.policy_id AND config->>'purpose' IN ('group_quality_target','cache_target')) THEN
  RAISE EXCEPTION 'group quality targets require active recovery; customer trial leases are retired';
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER passive_group_target_no_customer_trial BEFORE INSERT OR UPDATE ON passive_quality_leases
FOR EACH ROW EXECUTE FUNCTION reject_group_target_customer_trial();

CREATE FUNCTION reject_group_target_traffic_recovery() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 IF EXISTS(SELECT 1 FROM passive_quality_policies WHERE id=NEW.policy_id AND config->>'purpose' IN ('group_quality_target','cache_target')) THEN
  IF TG_OP='INSERT' THEN
   RAISE EXCEPTION 'group quality traffic recovery leases are retired';
  END IF;
  IF NEW.released_at IS NULL OR (NEW.fallback_expires_at IS DISTINCT FROM OLD.fallback_expires_at AND (NEW.fallback_expires_at IS NULL OR OLD.fallback_expires_at IS NULL OR NEW.fallback_expires_at>OLD.fallback_expires_at)) THEN
   RAISE EXCEPTION 'retired group quality traffic leases cannot reopen or renew';
  END IF;
 END IF;
 RETURN NEW;
END;
$$;
CREATE TRIGGER group_target_no_traffic_recovery BEFORE INSERT OR UPDATE ON group_quality_traffic_leases
FOR EACH ROW EXECUTE FUNCTION reject_group_target_traffic_recovery();

-- Replace only target-owned projections; retain manual and other policy holds.
WITH affected AS (SELECT DISTINCT account_id FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id WHERE p.config->>'purpose' IN ('group_quality_target','cache_target')),
projections AS (
 SELECT a.id,COALESCE((SELECT jsonb_agg(h) FROM (
  SELECT h FROM jsonb_array_elements(CASE WHEN jsonb_typeof(a.extra->'passive_quality')='array' THEN a.extra->'passive_quality' ELSE '[]'::jsonb END) h
  WHERE NOT EXISTS(SELECT 1 FROM passive_quality_policies p WHERE p.config->>'purpose' IN ('group_quality_target','cache_target') AND p.id::text=h->>'policy_id')
  UNION ALL
  SELECT jsonb_build_object('policy_id',p.id,'policy_name',p.config->>'name','scope',p.config->>'scope',
   'model',COALESCE(NULLIF(s.model_key,''),s.data->>'model',p.config->>'model'),
   'phase',s.data->>'phase','reason',s.data->>'reason','recovery_mode','active')
  FROM passive_quality_states s JOIN passive_quality_policies p ON p.id=s.policy_id
  WHERE s.account_id=a.id AND p.config->>'purpose' IN ('group_quality_target','cache_target')
  AND p.config->>'enabled'='true' AND p.config->>'mode'='enforce' AND s.data->>'phase' IN ('paused','probing','trial','limited')
  AND NOT EXISTS(SELECT 1 FROM jsonb_array_elements_text(COALESCE(NULLIF(p.config->'excluded_account_ids','null'::jsonb),'[]')) x WHERE x::bigint=a.id)
  AND EXISTS(SELECT 1 FROM account_groups ag JOIN groups g ON g.id=ag.group_id
   WHERE ag.account_id=a.id AND g.deleted_at IS NULL AND g.status='active' AND p.config->'group_ids' @> to_jsonb(ARRAY[ag.group_id]))
 ) holds),'[]'::jsonb) AS holds FROM accounts a JOIN affected ON affected.account_id=a.id
), changed AS (
 UPDATE accounts a SET extra=CASE WHEN p.holds='[]'::jsonb THEN COALESCE(a.extra,'{}')-'passive_quality' ELSE jsonb_set(COALESCE(a.extra,'{}'),'{passive_quality}',p.holds) END,updated_at=clock_timestamp()
 FROM projections p WHERE a.id=p.id RETURNING a.id
) INSERT INTO scheduler_outbox(event_type,account_id) SELECT 'account_changed',id FROM changed;
