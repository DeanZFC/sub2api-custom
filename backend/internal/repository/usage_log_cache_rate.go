package repository

// Keep eligibility aligned with channelMonitorV2CacheEligibleUL, including legacy rows.
const usageLogCacheEligible = "(COALESCE(request_type, 0) NOT IN (1, 4, 6) AND (stream = TRUE OR openai_ws_mode = TRUE))"
const usageLogCacheNumeratorSQL = "COALESCE(SUM(GREATEST(cache_read_tokens, 0)) FILTER (WHERE " + usageLogCacheEligible + "), 0)"
const usageLogCacheDenominatorSQL = "COALESCE(SUM(GREATEST(input_tokens::bigint - COALESCE(image_input_tokens, 0), 0) + GREATEST(cache_creation_tokens, 0) + GREATEST(cache_read_tokens, 0)) FILTER (WHERE " + usageLogCacheEligible + "), 0)"
const usageLogCacheRateSQL = usageLogCacheNumeratorSQL + "::double precision / NULLIF(" + usageLogCacheDenominatorSQL + ", 0)"
