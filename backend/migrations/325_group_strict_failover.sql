-- Opt-in strict primary/backup routing; existing groups retain their behavior.
ALTER TABLE groups ADD COLUMN IF NOT EXISTS strict_failover JSONB NOT NULL DEFAULT '{"enabled":false,"tiers":[]}'::jsonb;
