-- Freeze the roster at dispatch. Legacy executions have no source roster and
-- deliberately receive no new public history.
CREATE TABLE intelligence_rounds (
    run_id text PRIMARY KEY,
    plan_id bigint NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    started_at timestamptz NOT NULL,
    pending integer NOT NULL CHECK (pending >= 0),
    finished_at timestamptz
);
CREATE INDEX intelligence_rounds_plan_time ON intelligence_rounds(plan_id, started_at DESC);

-- An account can belong to two pools. Generate an independent, stable display
-- alias in each pool; account IDs and upstream identity never leave the server.
CREATE TABLE intelligence_account_aliases (
    group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    account_id bigint NOT NULL,
    alias text NOT NULL DEFAULT upper(substr(replace(gen_random_uuid()::text,'-',''),1,16)),
    PRIMARY KEY(group_id,account_id),
    UNIQUE (group_id,alias)
);
CREATE TABLE intelligence_round_targets (
    run_id text NOT NULL REFERENCES intelligence_rounds(run_id) ON DELETE CASCADE,
    group_id bigint NOT NULL REFERENCES groups(id) ON DELETE CASCADE,
    alias text NOT NULL,
    model text NOT NULL,
    effort text NOT NULL,
    result_id bigint REFERENCES scheduled_test_results(id) ON DELETE SET NULL,
    PRIMARY KEY(run_id,group_id,alias,model,effort)
);
CREATE INDEX intelligence_round_targets_group ON intelligence_round_targets(group_id,model,run_id);
CREATE INDEX intelligence_round_targets_result ON intelligence_round_targets(result_id);

-- Retry, worker completion and deletion update the same serialized counter.
-- A targeted recheck has its own run ID with no intelligence_rounds row.
CREATE FUNCTION update_intelligence_round_completion() RETURNS trigger LANGUAGE plpgsql AS $$
DECLARE
  delta integer;
  at_time timestamptz;
BEGIN
  IF TG_OP='DELETE' THEN
    IF OLD.status NOT IN ('pending','running') THEN RETURN OLD; END IF;
    delta := -1;
    at_time := now();
  ELSE
    delta := (NEW.status IN ('pending','running'))::integer - (OLD.status IN ('pending','running'))::integer;
    IF delta=0 THEN RETURN NEW; END IF;
    at_time := NEW.finished_at;
  END IF;
  -- A retry can fill a missing artwork on an already published result. Keep
  -- the original completion boundary and order while the retry is in flight.
  UPDATE intelligence_rounds SET pending=pending+delta,
    finished_at=COALESCE(finished_at,CASE WHEN pending+delta=0 THEN GREATEST(started_at,at_time,clock_timestamp()) END)
    WHERE run_id=OLD.run_id AND plan_id=OLD.plan_id;
  RETURN NULL;
END $$;
CREATE TRIGGER intelligence_round_status AFTER UPDATE OF status ON scheduled_test_results
    FOR EACH ROW WHEN (OLD.run_id<>'' AND NEW.status IS DISTINCT FROM OLD.status)
    EXECUTE FUNCTION update_intelligence_round_completion();
CREATE TRIGGER intelligence_round_deleted AFTER DELETE ON scheduled_test_results
    FOR EACH ROW WHEN (OLD.run_id<>'' AND OLD.status IN ('pending','running'))
    EXECUTE FUNCTION update_intelligence_round_completion();
