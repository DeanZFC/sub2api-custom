import type { UsageRequestTypeLike } from './usageRequestType'

type TokenCount = number | null | undefined

const normalizeTokenCount = (value: TokenCount): number => {
  const numeric = Number(value)
  return Number.isFinite(numeric) && numeric > 0 ? numeric : 0
}

/** Calculates cache reads as a share of all prompt tokens. */
export const calculateCacheHitRate = (
  inputTokens: TokenCount,
  cacheReadTokens: TokenCount,
  cacheCreationTokens: TokenCount,
): number | null => {
  const input = normalizeTokenCount(inputTokens)
  const cacheRead = normalizeTokenCount(cacheReadTokens)
  const cacheCreation = normalizeTokenCount(cacheCreationTokens)
  const promptTokens = input + cacheRead + cacheCreation

  return promptTokens > 0 ? cacheRead / promptTokens : null
}

export const formatCacheHitRate = (
  inputTokens: TokenCount,
  cacheReadTokens: TokenCount,
  cacheCreationTokens: TokenCount,
  fractionDigits = 1,
): string => {
  const rate = calculateCacheHitRate(inputTokens, cacheReadTokens, cacheCreationTokens)
  return rate == null ? '-' : `${(rate * 100).toFixed(fractionDigits)}%`
}

/** Match the backend cache-telemetry eligibility rule, including legacy rows. */
export const isCacheEligibleRequest = (request: UsageRequestTypeLike): boolean => {
  if (request.request_type === 'sync' || request.request_type === 'cyber') return false
  return request.stream === true || request.openai_ws_mode === true ||
    (request.openai_ws_mode == null && request.request_type === 'ws_v2')
}

export const formatCacheRate = (rate: number | null | undefined, fractionDigits = 1): string =>
  rate == null || !Number.isFinite(rate) ? '-' : (rate * 100).toFixed(fractionDigits) + '%'
