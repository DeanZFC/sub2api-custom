import { describe, expect, it } from 'vitest'
import { defaultPassivePolicy, passivePolicyDraft, passiveCacheValidation, upgradePassivePolicy } from '@/api/admin/passiveQuality'
import { applyQualityProfile } from '../qualityPolicyProfiles'

describe('independent protection configuration', () => {
  it('starts safely in observation with independent cache windows and nonzero validation traffic', () => {
    const p = defaultPassivePolicy()
    expect(p).toMatchObject({ schema_version: 2, mode: 'observe', scope: 'model', ttft_action: 'limit', cache_protection: { action: 'reduce', allocation_percent: 25, quick: { window_seconds: 300, max_samples: 50, min_samples: 15, confirmations: 2, new_samples: 3 }, action_window: { window_seconds: 900, max_samples: 200, min_samples: 30, confirmations: 3, new_samples: 5 } } })
    expect(passiveCacheValidation(p)).toBe('')
    expect(applyQualityProfile(p, 'responsive').cache_protection).toEqual(p.cache_protection)
  })
  it('does not upgrade legacy schemas on read, edit or profile selection', () => {
    const legacy = { ...defaultPassivePolicy(), schema_version: undefined, cache_protection: undefined, ttft_action: undefined, ttft_concurrency_limit: undefined, ttft_priority_penalty: undefined, mode: 'enforce' as const, scope: 'account' as const, id: 5, version: 4, model: 'model', min_samples: 42 }
    const draft = passivePolicyDraft(legacy)
    expect(draft.schema_version).toBe(0)
    expect(draft.cache_protection).toBeUndefined()
    expect(applyQualityProfile(draft, 'steady').schema_version).toBe(0)
    expect(passiveCacheValidation(draft)).toBe('')
    const upgraded = upgradePassivePolicy(draft)
    expect(upgraded).toMatchObject({ id: 5, version: 4, model: 'model', min_samples: 42, schema_version: 2, mode: 'observe', scope: 'model' })
    expect(draft.mode).toBe('enforce')
    expect(draft.scope).toBe('account')
  })
  it('keeps optional TPS protection off until selected and validates its independent action', () => {
    const p = defaultPassivePolicy()
    expect(p.tps.enabled).toBe(false)
    expect(passivePolicyDraft({ ...p, tps: { enabled: false, pause: 0, recover: 0 } }).tps).toEqual({ enabled: false, pause: 10, recover: 15 })
    p.tps.enabled = true
    expect(passiveCacheValidation(p)).toBe('')
    p.tps_concurrency_limit = 0
    expect(passiveCacheValidation(p)).toBe('invalidMetricActions')
    p.tps_concurrency_limit = 2
    expect(passiveCacheValidation(p)).toBe('')
    expect(upgradePassivePolicy({ ...p, schema_version: 0 })).toMatchObject({ tps_action: 'limit', tps_concurrency_limit: 2 })
  })

  it('rejects zero-allocation starvation and impossible independent-session evidence', () => {
    const p = defaultPassivePolicy()
    p.cache_protection!.allocation_percent = 0
    expect(passiveCacheValidation(p)).toBe('invalidCacheConfig')
    p.cache_protection!.allocation_percent = 25
    p.cache_protection!.quick.max_session_share_percent = 20
    expect(passiveCacheValidation(p)).toBe('invalidCacheWindow')
    p.cache_protection!.quick.max_session_share_percent = 60
    p.cache_protection!.quick.min_sessions = 8
    expect(passiveCacheValidation(p)).toBe('invalidCacheWindow')
    p.cache_protection!.quick.min_sessions = 3
    p.cache_protection!.action_window.min_tokens = 100
    expect(passiveCacheValidation(p)).toBe('invalidCacheOrder')
  })
  it('keeps the legacy account default when old payloads omit scope', () => {
    const legacy = { ...defaultPassivePolicy(), schema_version: 0, model: 'model-a' }
    Reflect.deleteProperty(legacy, 'scope')
    expect(passivePolicyDraft(legacy).scope).toBe('account')
    legacy.scope = 'model'
    expect(passivePolicyDraft(legacy).scope).toBe('model')
    legacy.model = ''
    expect(passivePolicyDraft(legacy).scope).toBe('account')
  })
  it('clones drafts so changing windows cannot mutate a loaded policy', () => {
    const p = defaultPassivePolicy(), draft = passivePolicyDraft(p)
    draft.cache_protection!.quick.min_samples = 25
    expect(p.cache_protection!.quick.min_samples).toBe(15)
  })
})
