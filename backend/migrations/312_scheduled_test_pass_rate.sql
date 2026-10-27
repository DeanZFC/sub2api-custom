-- Independent active capability evidence survives result-history pruning.
ALTER TABLE scheduled_test_results ADD COLUMN pass_rate_rule_version TEXT NOT NULL DEFAULT '';
CREATE TABLE scheduled_test_pass_rate_evidence (
 id BIGSERIAL PRIMARY KEY,
 result_id BIGINT NOT NULL UNIQUE,
 plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 test_definition_id BIGINT NOT NULL,
 model_key TEXT NOT NULL,
 rule_version TEXT NOT NULL,
 run_key TEXT NOT NULL,
 verdict TEXT NOT NULL CHECK(verdict IN ('pass','fail')),
 started_at TIMESTAMPTZ NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 UNIQUE(plan_id,account_id,test_definition_id,model_key,rule_version,run_key)
);
CREATE INDEX scheduled_test_pass_rate_window ON scheduled_test_pass_rate_evidence
 (plan_id,account_id,test_definition_id,model_key,rule_version,started_at DESC,id DESC);
CREATE TABLE scheduled_test_pass_rate_states (
 plan_id BIGINT NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
 account_id BIGINT NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
 test_definition_id BIGINT NOT NULL,
 model_key TEXT NOT NULL,
 rule_version TEXT NOT NULL,
 blocked BOOLEAN NOT NULL DEFAULT FALSE,
 summary JSONB NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
 PRIMARY KEY(plan_id,account_id,test_definition_id,model_key)
);
CREATE INDEX scheduled_test_pass_rate_holds ON scheduled_test_pass_rate_states(account_id,model_key) WHERE blocked;

-- Cascade deletion must remove only the deleted owners' routing projections.
-- Lock each account before reading surviving holds so concurrent plan writers
-- cannot publish a stale projection. Transition rows handle batch deletes once
-- per account and acquire account locks in the same order as Go writers.
CREATE FUNCTION reconcile_deleted_scheduled_test_pass_rate_holds() RETURNS trigger
LANGUAGE plpgsql AS $$
DECLARE
 affected_account BIGINT;
 remaining_holds JSONB;
 changed_count BIGINT;
BEGIN
 FOR affected_account IN SELECT DISTINCT account_id FROM deleted_pass_rate_states ORDER BY account_id LOOP
  PERFORM id FROM accounts WHERE id=affected_account FOR NO KEY UPDATE;
  IF NOT FOUND THEN
   CONTINUE;
  END IF;
  SELECT COALESCE(jsonb_object_agg(model_key,reason),'{}'::jsonb) INTO remaining_holds FROM (
   SELECT DISTINCT ON(model_key) model_key,COALESCE(summary->>'reason','recovery_pending') AS reason
   FROM scheduled_test_pass_rate_states WHERE account_id=affected_account AND blocked
   ORDER BY model_key,updated_at DESC
  ) holds;
  UPDATE accounts SET extra=CASE WHEN remaining_holds='{}'::jsonb
    THEN COALESCE(extra,'{}'::jsonb)-'active_pass_rate_holds'
    ELSE jsonb_set(COALESCE(extra,'{}'::jsonb),'{active_pass_rate_holds}',remaining_holds) END,
   updated_at=clock_timestamp()
  WHERE id=affected_account AND COALESCE(extra->'active_pass_rate_holds','{}'::jsonb) IS DISTINCT FROM remaining_holds;
  GET DIAGNOSTICS changed_count = ROW_COUNT;
  IF changed_count > 0 THEN
   INSERT INTO scheduler_outbox(event_type,account_id) VALUES('account_changed',affected_account);
  END IF;
 END LOOP;
 RETURN NULL;
END;
$$;
CREATE TRIGGER scheduled_test_pass_rate_deleted_projection
AFTER DELETE ON scheduled_test_pass_rate_states
REFERENCING OLD TABLE AS deleted_pass_rate_states
FOR EACH STATEMENT EXECUTE FUNCTION reconcile_deleted_scheduled_test_pass_rate_holds();
