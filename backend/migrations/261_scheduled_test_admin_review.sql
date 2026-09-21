ALTER TABLE scheduled_test_protection_states
    ADD COLUMN IF NOT EXISTS admin_verdict TEXT NOT NULL DEFAULT '' CHECK (admin_verdict IN ('', 'pass', 'fail')),
    ADD COLUMN IF NOT EXISTS admin_user_id BIGINT REFERENCES users(id) ON DELETE SET NULL,
    ADD COLUMN IF NOT EXISTS admin_decided_at TIMESTAMPTZ;
