-- Allow administrators to control the order of test type tabs shown to users.
ALTER TABLE scheduled_test_definitions
    ADD COLUMN IF NOT EXISTS sort_order INTEGER NOT NULL DEFAULT 0;

-- Preserve the existing definition order when upgrading older installations.
UPDATE scheduled_test_definitions
SET sort_order = id
WHERE sort_order = 0;

CREATE INDEX IF NOT EXISTS idx_std_sort_order
    ON scheduled_test_definitions(sort_order, id);
