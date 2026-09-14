-- Indexes for the administrator's paginated shared-pool revenue views.
CREATE INDEX IF NOT EXISTS idx_shared_usage_consumer_created
    ON shared_account_usage_ledger(consumer_user_id, created_at DESC)
    WHERE action = 'earn';

CREATE INDEX IF NOT EXISTS idx_shared_usage_owner_action_created
    ON shared_account_usage_ledger(owner_user_id, action, created_at DESC);

CREATE INDEX IF NOT EXISTS idx_shared_call_stats_model_created
    ON shared_account_call_stats(model, created_at DESC);
