ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS user_concurrency_limit INTEGER NOT NULL DEFAULT 0;

DO $$
BEGIN
    IF NOT EXISTS (
        SELECT 1 FROM pg_constraint
        WHERE conname = 'groups_user_concurrency_limit_nonnegative'
          AND conrelid = 'groups'::regclass
    ) THEN
        ALTER TABLE groups
            ADD CONSTRAINT groups_user_concurrency_limit_nonnegative
            CHECK (user_concurrency_limit >= 0);
    END IF;
END $$;

COMMENT ON COLUMN groups.user_concurrency_limit IS
    'Per-user concurrent request limit within this group; 0 means unlimited';
