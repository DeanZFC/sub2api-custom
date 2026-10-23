import { describe, expect, it } from 'vitest'
import { defaultPassivePolicy } from '@/api/admin/passiveQuality'
import { applyQualityProfile, matchingQualityProfile, policyResponse, qualityProfileKeys, setPolicyResponse } from '../qualityPolicyProfiles'

describe('quality configuration profiles', () => {
  it('recognizes existing defaults without changing policy identity or behavior', () => {
    const policy = defaultPassivePolicy()
    expect(matchingQualityProfile(policy)).toBe('balanced')
    expect(applyQualityProfile(policy, 'balanced')).toEqual(policy)
    expect(policyResponse(policy)).toBe('observe')
  })

  it.each(qualityProfileKeys)('keeps targets, thresholds and enforcement intact for %s', profile => {
    const policy = { ...defaultPassivePolicy(), id: 7, version: 12, name: 'Custom', enabled: false, group_ids: [4, 5], excluded_account_ids: [8], mode: 'enforce' as const, action: 'limit' as const, model: 'model-a', scope: 'model' as const, concurrency_limit: 4, priority_penalty: 19, error_rate: { enabled: true, pause: 17, recover: 3 } }
    const before = JSON.stringify(policy)
    const next = applyQualityProfile(policy, profile)
    for (const key of ['id', 'version', 'name', 'enabled', 'group_ids', 'excluded_account_ids', 'mode', 'action', 'model', 'scope', 'concurrency_limit', 'priority_penalty', 'error_rate', 'cache_rate', 'ttft'] as const) expect(next[key]).toEqual(policy[key])
    expect(JSON.stringify(policy)).toBe(before)
    expect(matchingQualityProfile(next)).toBe(profile)
    expect(next.min_samples).toBeLessThanOrEqual(next.max_samples)
    expect(next.recovery_samples).toBeLessThanOrEqual(next.trial_requests)
    expect(next.recovery_samples).toBeLessThanOrEqual(next.max_samples)
    expect(next.max_cooldown_seconds).toBeGreaterThanOrEqual(next.cooldown_seconds)
    expect(next.new_samples).toBeGreaterThan(0)
    expect(next.confirmations).toBeGreaterThan(1)
  })

  it('labels custom values without replacing them', () => {
    const policy = { ...defaultPassivePolicy(), window_seconds: 1234, trial_concurrency: 3 }
    expect(matchingQualityProfile(policy)).toBe('custom')
    expect(policy.window_seconds).toBe(1234)
    expect(policy.trial_concurrency).toBe(3)
  })

  it('changes responses explicitly and preserves the configured action during observation', () => {
    const policy = defaultPassivePolicy()
    const limited = setPolicyResponse(policy, 'limit')
    expect(limited).toMatchObject({ mode: 'enforce', action: 'limit' })
    expect(policyResponse(limited)).toBe('limit')
    const observed = setPolicyResponse(limited, 'observe')
    expect(observed).toMatchObject({ mode: 'observe', action: 'limit' })
    expect(policyResponse(observed)).toBe('observe')
    expect(setPolicyResponse(observed, 'pause')).toMatchObject({ mode: 'enforce', action: 'pause' })
    expect(policy).toEqual(defaultPassivePolicy())
  })
})
