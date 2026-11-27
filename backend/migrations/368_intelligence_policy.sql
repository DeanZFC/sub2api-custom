CREATE TABLE intelligence_observations (
    result_id bigint PRIMARY KEY,
    plan_id bigint NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_key text NOT NULL,
    policy_version text NOT NULL,
    run_id text NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    verdict text NOT NULL CHECK(verdict IN ('pass','suspect','confirmed','unknown')),
    routing_veto boolean NOT NULL DEFAULT false,
    released boolean NOT NULL DEFAULT false,
    deferred_until timestamptz,
    auto_isolate boolean NOT NULL,
    recovery_passes integer NOT NULL,
    cooldown_minutes integer NOT NULL,
    UNIQUE(plan_id,account_id,model_key,run_id)
);
CREATE INDEX intelligence_target_history ON intelligence_observations(plan_id,account_id,model_key,started_at,result_id);
CREATE TABLE intelligence_artwork_reviews (
    result_id bigint PRIMARY KEY REFERENCES intelligence_observations(result_id) ON DELETE CASCADE,
    verdict text NOT NULL CHECK(verdict IN ('pass','fail')),
    admin_id bigint NOT NULL REFERENCES users(id),
    reason text NOT NULL DEFAULT '',
    reviewed_at timestamptz NOT NULL DEFAULT now()
);
CREATE TABLE intelligence_model_states (
    plan_id bigint NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_key text NOT NULL,
    last_result_id bigint NOT NULL DEFAULT 0,
    last_started_at timestamptz,
    policy_version text NOT NULL DEFAULT '',
    hold_version text NOT NULL DEFAULT '',
    blocked boolean NOT NULL,
    passes integer NOT NULL,
    verdict text NOT NULL,
    next_check timestamptz,
    updated_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY(plan_id,account_id,model_key)
);
CREATE INDEX intelligence_due ON intelligence_model_states(next_check,plan_id) WHERE next_check IS NOT NULL;
CREATE INDEX intelligence_account_holds ON intelligence_model_states(account_id,plan_id) WHERE blocked;
CREATE TABLE intelligence_artwork_review_log (
    id bigserial PRIMARY KEY,
    result_id bigint NOT NULL REFERENCES intelligence_observations(result_id) ON DELETE CASCADE,
    verdict text NOT NULL CHECK(verdict IN ('pass','fail')),
    admin_id bigint NOT NULL REFERENCES users(id),
    reason text NOT NULL DEFAULT '',
    reviewed_at timestamptz NOT NULL DEFAULT now()
);
ALTER TABLE quality_probe_attempts ADD COLUMN IF NOT EXISTS intelligence_probe boolean NOT NULL DEFAULT false;
CREATE INDEX intelligence_budget_day ON quality_probe_attempts(day,account_id) WHERE intelligence_probe;
CREATE INDEX scheduled_intelligence_missing_art ON scheduled_test_results(finished_at,id) WHERE status='failed' AND execution_info->'intelligence'->>'artwork'='missing';
CREATE TABLE intelligence_release_log (
    id bigserial PRIMARY KEY,
    plan_id bigint NOT NULL REFERENCES scheduled_test_plans(id) ON DELETE CASCADE,
    account_id bigint NOT NULL REFERENCES accounts(id) ON DELETE CASCADE,
    model_key text NOT NULL,
    admin_id bigint NOT NULL REFERENCES users(id),
    released_at timestamptz NOT NULL DEFAULT now()
);
