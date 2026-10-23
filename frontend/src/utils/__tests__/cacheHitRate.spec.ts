import { describe, expect, it } from 'vitest'
import { calculateCacheHitRate, formatCacheRate, isCacheEligibleRequest } from '../cacheHitRate'

describe('cache eligibility', () => {
  it.each([
    [{ request_type: 'sync', stream: false }, false],
    [{ request_type: 'sync', stream: true, openai_ws_mode: true }, false],
    [{ request_type: 'cyber', stream: true }, false],
    [{ request_type: 'stream', stream: true }, true],
    [{ request_type: 'stream', stream: false }, false],
    [{ request_type: 'ws_v2', stream: false, openai_ws_mode: true }, true],
    [{ request_type: 'ws_v2', stream: false }, true],
    [{ request_type: 'unknown', stream: false }, false],
    [{ request_type: 'unknown', stream: true }, true],
    [{ stream: false, openai_ws_mode: true }, true],
    [{ stream: false }, false],
    [{}, false],
  ] as const)('classifies %j', (request, expected) => {
    expect(isCacheEligibleRequest(request)).toBe(expected)
  })

  it('keeps zero hits distinct from an empty sample', () => {
    expect(calculateCacheHitRate(100, 0, 0)).toBe(0)
    expect(calculateCacheHitRate(0, 0, 0)).toBeNull()
    expect(calculateCacheHitRate(10, 70, 20)).toBe(0.7)
    expect(formatCacheRate(0)).toBe('0.0%')
    expect(formatCacheRate(null)).toBe('-')
    expect(formatCacheRate(undefined)).toBe('-')
  })
})
