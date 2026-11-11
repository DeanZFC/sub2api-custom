ALTER TABLE groups
    ADD COLUMN IF NOT EXISTS force_openai_service_tier VARCHAR(16) NOT NULL DEFAULT ''
    CHECK (force_openai_service_tier IN ('', 'priority', 'ultrafast'));

UPDATE groups
SET force_openai_service_tier = 'priority'
WHERE force_openai_fast = TRUE AND force_openai_service_tier = '';

COMMENT ON COLUMN groups.force_openai_service_tier IS
    'Force OpenAI gateway requests to priority or ultrafast before global policy evaluation; empty preserves client requests';

-- Preserve the durable cache invalidation backstop for admin or direct SQL edits.
CREATE OR REPLACE FUNCTION enqueue_group_auth_cache_invalidation()
RETURNS TRIGGER
LANGUAGE plpgsql
AS $$
DECLARE
    target_group_id BIGINT;
BEGIN
    target_group_id := OLD.id;
    IF TG_OP = 'UPDATE'
       AND OLD.status IS NOT DISTINCT FROM NEW.status
       AND OLD.is_exclusive IS NOT DISTINCT FROM NEW.is_exclusive
       AND OLD.allow_image_generation IS NOT DISTINCT FROM NEW.allow_image_generation
       AND OLD.platform IS NOT DISTINCT FROM NEW.platform
       AND OLD.subscription_type IS NOT DISTINCT FROM NEW.subscription_type
       AND OLD.rate_multiplier IS NOT DISTINCT FROM NEW.rate_multiplier
       AND OLD.peak_rate_enabled IS NOT DISTINCT FROM NEW.peak_rate_enabled
       AND OLD.peak_start IS NOT DISTINCT FROM NEW.peak_start
       AND OLD.peak_end IS NOT DISTINCT FROM NEW.peak_end
       AND OLD.peak_rate_multiplier IS NOT DISTINCT FROM NEW.peak_rate_multiplier
       AND OLD.profit_control_enabled IS NOT DISTINCT FROM NEW.profit_control_enabled
       AND OLD.profit_min_margin IS NOT DISTINCT FROM NEW.profit_min_margin
       AND OLD.profit_safety_buffer IS NOT DISTINCT FROM NEW.profit_safety_buffer
       AND OLD.force_openai_service_tier IS NOT DISTINCT FROM NEW.force_openai_service_tier
       AND OLD.force_openai_fast IS NOT DISTINCT FROM NEW.force_openai_fast
       AND OLD.deleted_at IS NOT DISTINCT FROM NEW.deleted_at THEN
        RETURN NEW;
    END IF;

    INSERT INTO auth_cache_invalidation_outbox (cache_key)
    SELECT api_key_cache_digest(k.key)
    FROM api_keys AS k
    WHERE (k.group_id = target_group_id OR k.fallback_group_id = target_group_id)
      AND k.deleted_at IS NULL
      AND k.key <> '';
    IF TG_OP = 'DELETE' THEN
        RETURN OLD;
    END IF;
    RETURN NEW;
END;
$$;
