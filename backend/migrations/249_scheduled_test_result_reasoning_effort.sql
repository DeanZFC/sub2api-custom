-- Preserve the selected reasoning effort on each scheduled test result so
-- historical results remain self-describing after a plan is edited.
ALTER TABLE scheduled_test_results
    ADD COLUMN IF NOT EXISTS reasoning_effort VARCHAR(20) NOT NULL DEFAULT '';
