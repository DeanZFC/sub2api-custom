import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import type { PassiveActiveRecoveryResult } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import zhResources from '@/i18n/locales/zh/admin/resources'
import ActiveRecoveryEvidence from '../ActiveRecoveryEvidence.vue'

const result = (overrides: Partial<PassiveActiveRecoveryResult> = {}): PassiveActiveRecoveryResult => ({
  verdict: 'pending', passed: false, reason: 'insufficient_active_recovery_batches', requests: 12, warmup_requests: 2, batches: 2,
  started_at: '2026-10-24T00:00:00Z', finished_at: '2026-10-24T00:02:01Z',
  evidence: { samples: 12, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: 1234, ttft_samples: 12, cache_rate: 0, cache_samples: 10, output_tps: null, tps_samples: 0, fresh: 12, complete: true, latest: '2026-10-24T00:02:01Z' },
  ...overrides,
})
const render = (value = result(), locale = 'en', recoveredAt?: string) => mount(ActiveRecoveryEvidence, {
  props: { result: value, recoveredAt },
  global: { plugins: [createI18n({ legacy: false, locale, missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources }, zh: { admin: zhResources } } })] },
})

describe('active recovery evidence', () => {
  it('separates system evidence and renders real zero versus missing measurements', () => {
    const w = render()
    expect(w.text()).toContain('System requests 12 · Warmup 2 · Verification batches 2')
    expect(w.text()).toContain('Availability: 100%')
    expect(w.text()).toContain('Actual cache usage: 0%')
    expect(w.text()).toContain('First-token latency P90: 1.234 s')
    expect(w.text()).toContain('Output TPS: Unknown')
    expect(w.text()).toContain('excluded from the one-hour production metrics')
    expect(w.text()).toContain('more valid batches, samples or elapsed evidence time')
    w.unmount()
  })

  it('shows postponed checks and the separate successful recovery timestamp', () => {
    const w = render(result({ verdict: 'deferred', reason: 'account_unavailable_for_recovery', deferred_until: '2026-10-24T00:05:00Z' }), 'en', '2026-10-23T23:00:00Z')
    expect(w.text()).toContain('Waiting for budget or execution conditions')
    expect(w.text()).toContain('Next active check:')
    expect(w.text()).toContain('Last active recovery:')
    expect(w.text()).not.toContain('account_unavailable_for_recovery')
    w.unmount()
  })

  it('localizes Chinese evidence and omits unset recovery timestamps', () => {
    const w = render(result({ verdict: 'pass', passed: true, reason: 'active_recovery_passed' }), 'zh', '0001-01-01T00:00:00Z')
    expect(w.text()).toContain('最近主动复测 · 验证通过')
    expect(w.text()).toContain('已达到恢复阈值')
    expect(w.text()).not.toContain('上次主动验证恢复')
    w.unmount()
  })
})
