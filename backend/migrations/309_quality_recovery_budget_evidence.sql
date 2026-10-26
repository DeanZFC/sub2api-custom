-- Recovery receives a reserved share inside the existing hard daily caps.
-- Absent recovery_reserve_percent remains zero for existing configurations.
ALTER TABLE IF EXISTS quality_budget_days
 ADD COLUMN IF NOT EXISTS recovery_requests BIGINT NOT NULL DEFAULT 0,
 ADD COLUMN IF NOT EXISTS recovery_tokens BIGINT NOT NULL DEFAULT 0;
ALTER TABLE IF EXISTS quality_probe_attempts
 ADD COLUMN IF NOT EXISTS purpose TEXT NOT NULL DEFAULT 'regular' CHECK (purpose IN ('regular','recovery'));

-- Evidence survives round replacement and pruning of its original result.
-- No cascading foreign keys: audit identifiers are historical references.
CREATE TABLE IF NOT EXISTS scheduled_test_protection_events (
 id BIGSERIAL PRIMARY KEY,
 plan_id BIGINT NOT NULL, account_id BIGINT NOT NULL,
 test_definition_id BIGINT NOT NULL DEFAULT 0, result_id BIGINT NOT NULL,
 created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(), snapshot JSONB NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_scheduled_test_protection_events_account
 ON scheduled_test_protection_events(account_id,created_at DESC,id DESC);
ALTER TABLE IF EXISTS scheduled_test_protection_states ADD COLUMN IF NOT EXISTS trigger_event_id BIGINT;
ALTER TABLE IF EXISTS scheduled_test_combination_states ADD COLUMN IF NOT EXISTS trigger_event_id BIGINT;

CREATE OR REPLACE FUNCTION reject_quality_evidence_update() RETURNS trigger LANGUAGE plpgsql AS $$
BEGIN
 RAISE EXCEPTION 'quality protection evidence is immutable';
END;
$$;
DROP TRIGGER IF EXISTS scheduled_test_protection_events_immutable ON scheduled_test_protection_events;
CREATE TRIGGER scheduled_test_protection_events_immutable BEFORE UPDATE ON scheduled_test_protection_events
 FOR EACH ROW EXECUTE FUNCTION reject_quality_evidence_update();
