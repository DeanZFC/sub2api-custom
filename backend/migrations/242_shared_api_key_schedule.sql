ALTER TABLE shared_api_keys
    ADD COLUMN IF NOT EXISTS selection_mode VARCHAR(20) NOT NULL DEFAULT 'manual',
    ADD COLUMN IF NOT EXISTS priority_mode VARCHAR(20) NOT NULL DEFAULT 'order';

UPDATE shared_api_keys
SET selection_mode = 'manual',
    priority_mode = 'order'
WHERE selection_mode IS NULL OR priority_mode IS NULL;
