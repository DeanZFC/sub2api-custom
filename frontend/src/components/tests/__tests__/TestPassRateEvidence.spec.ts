import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import type { TestPassRateSummary } from '@/types'
import resources from '@/i18n/locales/en/admin/resources'
import TestPassRateEvidence from '../TestPassRateEvidence.vue'

const evidence = (extra: Partial<TestPassRateSummary> = {}): TestPassRateSummary => ({
  samples: 10, passed: 7, pass_rate: 70, window_size: 10, window_seconds: 604800,
  pause_below: 80, recover_at_least: 90, recover_consecutive: 2, consecutive_passes: 1,
  blocked: true, ready: true, stale: false, reason: 'pass_rate_below_threshold',
  latest_at: '2026-10-27T10:00:00Z', latest_verdict: 'pass', rule_version: 'v1', model: 'test-model', ...extra,
})
const mountEvidence = (value = evidence()) => mount(TestPassRateEvidence, {
  props: { evidence: value },
  global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: resources } } })] },
})

describe('recent capability pass-rate evidence', () => {
  it('shows current evidence and concrete recovery requirements independently of the latest verdict', () => {
    const wrapper = mountEvidence()
    expect(wrapper.get('[data-pass-rate-state]').text()).toBe('Calls paused')
    expect(wrapper.get('[data-pass-rate-counts]').text()).toBe('7/10 passed · 70%')
    expect(wrapper.get('[data-pass-rate-recovery-requirement]').text()).toContain('90% passing and 2 consecutive passes (currently 1)')
    expect(wrapper.get('[data-pass-rate-reason]').text()).toContain('below the pause threshold')
    expect(wrapper.text()).toContain('current window, not the evidence as of this historical result')
    expect(wrapper.text()).toContain('test-model')
    wrapper.unmount()
  })

  it('keeps stale blocked evidence distinct from recovery and never presents absent samples as zero percent', () => {
    const wrapper = mountEvidence(evidence({ samples: 0, passed: 0, pass_rate: null, ready: false, stale: true, latest_at: undefined, latest_verdict: '', reason: 'stale_evidence' }))
    expect(wrapper.get('[data-pass-rate-state]').text()).toBe('Calls paused')
    expect(wrapper.get('[data-pass-rate-counts]').text()).toBe('0/0 passed · —')
    expect(wrapper.get('[data-pass-rate-collecting]').text()).toContain('10 more valid checks')
    expect(wrapper.get('[data-pass-rate-stale]').text()).toContain('expiry does not remove an existing hold')
    wrapper.unmount()
  })
})
