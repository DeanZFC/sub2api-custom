ALTER TABLE scheduled_test_protection_states
    ADD COLUMN IF NOT EXISTS recovery_phase TEXT NOT NULL DEFAULT ''
        CHECK (recovery_phase IN ('', 'cooldown', 'trial')),
    ADD COLUMN IF NOT EXISTS recovery_cooldown_until TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recovery_trial_started_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recovery_trial_ends_at TIMESTAMPTZ,
    ADD COLUMN IF NOT EXISTS recovery_trial_requests BIGINT NOT NULL DEFAULT 0
        CHECK (recovery_trial_requests >= 0);

CREATE INDEX IF NOT EXISTS idx_st_protection_recovery_due
    ON scheduled_test_protection_states (updated_at, plan_id, account_id, test_definition_id)
    WHERE blocked AND recovery_phase <> '';
