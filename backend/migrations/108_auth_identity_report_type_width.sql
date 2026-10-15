-- Must sort after foundation_core and before 109's backfill. The backfill
-- emits 'oidc_synthetic_email_requires_manual_recovery', which
-- cannot be coerced into the original VARCHAR(40), even on an empty database.
-- Keep the already-published migrations immutable for existing installations.
ALTER TABLE auth_identity_migration_reports ALTER COLUMN report_type TYPE TEXT;
