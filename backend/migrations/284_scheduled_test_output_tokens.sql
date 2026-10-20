-- Zero preserves each protocol's existing test budget. Only the built-in
-- Pelican test needs a larger default to finish its HTML/SVG output.
ALTER TABLE scheduled_test_definitions
    ADD COLUMN IF NOT EXISTS max_output_tokens INTEGER NOT NULL DEFAULT 0
    CHECK (max_output_tokens BETWEEN 0 AND 131072);

UPDATE scheduled_test_definitions SET max_output_tokens = 32768
WHERE key = 'pelican' AND max_output_tokens = 0;
