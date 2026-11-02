-- Keep historical results and quality audit references when an administrator
-- removes a test type from the management catalog.
ALTER TABLE scheduled_test_definitions
    ADD COLUMN IF NOT EXISTS deleted_at TIMESTAMPTZ;

CREATE INDEX IF NOT EXISTS idx_scheduled_test_definitions_not_deleted
    ON scheduled_test_definitions (sort_order, id) WHERE deleted_at IS NULL;

-- Plan writes through the API serialize with archival in the repository. This
-- trigger also rejects direct SQL writes and locks selected definitions so a
-- concurrent archive cannot slip between validation and insertion.
CREATE OR REPLACE FUNCTION reject_archived_scheduled_test_definitions()
RETURNS TRIGGER LANGUAGE plpgsql AS $$
DECLARE
    selected_id BIGINT;
BEGIN
    FOR selected_id IN
        SELECT DISTINCT id FROM unnest(
            COALESCE(NEW.test_definition_ids, '{}'::bigint[]) ||
            CASE WHEN NEW.test_definition_id IS NULL THEN '{}'::bigint[]
                 ELSE ARRAY[NEW.test_definition_id] END
        ) AS selected(id)
        ORDER BY id
    LOOP
        PERFORM 1 FROM scheduled_test_definitions
        WHERE id = selected_id AND deleted_at IS NULL FOR KEY SHARE;
        IF NOT FOUND THEN
            RAISE EXCEPTION 'test definition % has been deleted', selected_id
                USING ERRCODE = '23503';
        END IF;
    END LOOP;
    RETURN NEW;
END;
$$;

DROP TRIGGER IF EXISTS trg_stp_reject_archived_definitions ON scheduled_test_plans;
CREATE TRIGGER trg_stp_reject_archived_definitions
    BEFORE INSERT OR UPDATE OF test_definition_id, test_definition_ids
    ON scheduled_test_plans FOR EACH ROW
    EXECUTE FUNCTION reject_archived_scheduled_test_definitions();
