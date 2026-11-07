-- Replace the retired process-local HTTP monitor. Keep audit history and only
-- clear the pause owned by this feature; never change accounts.schedulable.
ALTER TABLE account_performance_monitors ADD COLUMN IF NOT EXISTS trial_started_at TIMESTAMPTZ;
ALTER TABLE account_performance_monitors ADD COLUMN IF NOT EXISTS trial_requests BIGINT NOT NULL DEFAULT 0;
ALTER TABLE account_performance_monitors ADD COLUMN IF NOT EXISTS next_trial_at TIMESTAMPTZ;
ALTER TABLE account_performance_monitors ADD COLUMN IF NOT EXISTS metrics_since TIMESTAMPTZ;
ALTER TABLE account_performance_events DROP CONSTRAINT IF EXISTS account_performance_events_action_check;
ALTER TABLE account_performance_events ADD CONSTRAINT account_performance_events_action_check
 CHECK(action IN ('configure','pause','release','trial','recover','retire'));
INSERT INTO account_performance_events(account_id,action,revision,pause_generation,evidence)
 SELECT account_id,'retire',revision+1,pause_generation,jsonb_build_object('old_config',config,'was_paused',paused)
 FROM account_performance_monitors WHERE COALESCE(config->>'version','')<>'2';
UPDATE account_performance_monitors SET config='{"version":2,"enabled":false,"min_samples":20,"auto_recover":true,"cooldown_minutes":5,"recovery_samples":3}',
 revision=revision+1,paused=false,paused_at=NULL,reason='',updated_at=NOW()
 WHERE COALESCE(config->>'version','')<>'2';
ALTER TABLE account_performance_monitors ALTER COLUMN config SET DEFAULT '{"version":2,"enabled":false,"min_samples":20,"auto_recover":true,"cooldown_minutes":5,"recovery_samples":3}';
DELETE FROM settings WHERE key='account_performance_window_minutes';
