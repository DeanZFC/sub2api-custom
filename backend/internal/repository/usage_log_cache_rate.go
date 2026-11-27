package repository

// Cache eligibility for usage reports, which also contain non-streaming requests.
const usageLogCacheEligible = "(COALESCE(request_type, 0) NOT IN (1, 4, 6) AND (stream = TRUE OR openai_ws_mode = TRUE) AND (api_key_id IS NOT NULL OR (quality_usage->>'cache_read_tokens' IS NOT NULL AND quality_usage->>'input_tokens' IS NOT NULL)))"
const usageLogCacheNumeratorSQL = "COALESCE(SUM(GREATEST(cache_read_tokens, 0)) FILTER (WHERE " + usageLogCacheEligible + "), 0)"
const usageLogCacheDenominatorSQL = "COALESCE(SUM(GREATEST(input_tokens::bigint - COALESCE(image_input_tokens, 0), 0) + GREATEST(cache_creation_tokens, 0) + GREATEST(cache_read_tokens, 0)) FILTER (WHERE " + usageLogCacheEligible + "), 0)"
const usageLogCacheRateSQL = usageLogCacheNumeratorSQL + "::double precision / NULLIF(" + usageLogCacheDenominatorSQL + ", 0)"
