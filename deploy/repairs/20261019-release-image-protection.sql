-- Incident: image accounts 2280/2283 moved from Pro policy 22 to group 51.
-- Run with psql -X -v ON_ERROR_STOP=1. This is NOT an automatic migration.
-- Check all preconditions before removing only this policy's account state.
-- Any error aborts the transaction; original test results remain intact.
\set ON_ERROR_STOP on
BEGIN;
SET LOCAL lock_timeout = '5s';
SET LOCAL statement_timeout = '15s';

-- Match application lock ordering: policy configuration -> plan -> accounts.
SELECT pg_advisory_xact_lock(1937006960, 1735553655);
SELECT id FROM scheduled_test_plans WHERE id = 22 FOR NO KEY UPDATE;
SELECT id FROM accounts WHERE id IN (2280, 2283) ORDER BY id FOR UPDATE;
SELECT account_id, group_id FROM account_groups
WHERE account_id IN (2280, 2283) ORDER BY account_id, group_id FOR UPDATE;

DO $repair$
BEGIN
  IF NOT EXISTS (SELECT 1 FROM scheduled_test_plans WHERE id = 22) THEN
    RAISE EXCEPTION 'Policy 22 does not exist; no changes made';
  END IF;
  IF (SELECT count(*) FROM accounts WHERE id IN (2280, 2283)
      AND deleted_at IS NULL AND schedulable
      AND status IN ('active', 'quality_paused')) <> 2 THEN
    RAISE EXCEPTION 'Target accounts changed or were manually disabled; no changes made';
  END IF;
  IF NOT EXISTS (SELECT 1 FROM groups WHERE id = 51 AND status = 'active' AND deleted_at IS NULL)
     OR (SELECT count(DISTINCT account_id) FROM account_groups
         WHERE account_id IN (2280, 2283) AND group_id = 51) <> 2
     OR EXISTS (SELECT 1 FROM account_groups WHERE account_id IN (2280, 2283) AND group_id <> 51)
     OR EXISTS (SELECT 1 FROM scheduled_test_plans WHERE id = 22 AND 51 = ANY(group_ids)) THEN
    RAISE EXCEPTION 'Both accounts must belong only to group 51 outside policy 22; no changes made';
  END IF;
  IF EXISTS (SELECT 1 FROM scheduled_test_results r JOIN scheduled_test_plans p ON p.id = r.plan_id
             WHERE r.plan_id = 22 AND r.account_id IN (2280, 2283)
               AND r.run_id = p.latest_run_id AND r.run_id <> ''
               AND r.status IN ('pending', 'running')) THEN
    RAISE EXCEPTION 'Policy 22 still has pending/running attempts for these accounts; wait for that round to finish';
  END IF;
  IF EXISTS (SELECT 1 FROM scheduled_test_protection_states
             WHERE account_id IN (2280, 2283) AND plan_id <> 22 AND blocked)
     OR EXISTS (SELECT 1 FROM scheduled_test_combination_states
                WHERE account_id IN (2280, 2283) AND plan_id <> 22 AND blocked) THEN
    RAISE EXCEPTION 'Another policy still holds these accounts; no changes made';
  END IF;
END;
$repair$;

DELETE FROM scheduled_test_protection_states
WHERE plan_id = 22 AND account_id IN (2280, 2283);
DELETE FROM scheduled_test_combination_states
WHERE plan_id = 22 AND account_id IN (2280, 2283);

UPDATE accounts
SET status = 'active',
    extra = COALESCE(extra, '{}'::jsonb) - 'quality_protection_reason' - 'quality_protection_trial',
    updated_at = NOW()
WHERE id IN (2280, 2283);
INSERT INTO scheduler_outbox (event_type, account_id)
SELECT 'account_changed', id FROM accounts WHERE id IN (2280, 2283);

-- Only a final COMMIT confirms the repair was saved.
SELECT a.id, a.status, a.schedulable, a.concurrency,
       EXISTS (SELECT 1 FROM scheduled_test_protection_states s
               WHERE s.account_id = a.id AND s.blocked) AS protection_blocked,
       EXISTS (SELECT 1 FROM scheduled_test_combination_states s
               WHERE s.account_id = a.id AND s.blocked) AS combination_blocked
FROM accounts a WHERE a.id IN (2280, 2283) ORDER BY a.id;
COMMIT;
