import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import type { TestPassRateSummary } from '@/types'
import resources from '@/i18n/locales/en/admin/resources'
import zhResources from '@/i18n/locales/zh/admin/resources'
import TestRecentPassRate from '../TestRecentPassRate.vue'

const evidence = (extra: Partial<TestPassRateSummary> = {}): TestPassRateSummary => ({
  samples: 12, passed: 8, pass_rate: 66.6667, window_size: 20, window_seconds: 129600,
  pause_below: 80, recover_at_least: 90, recover_consecutive: 2, consecutive_passes: 1,
  blocked: false, ready: false, stale: false, reason: 'insufficient_samples',
  latest_at: '2026-10-27T10:00:00Z', latest_verdict: 'pass', rule_version: 'v1', model: 'test-model', ...extra,
})
const mountRecentRate = (value?: TestPassRateSummary | null, locale = 'en') => mount(TestRecentPassRate, {
  props: { evidence: value },
  global: { plugins: [createI18n({ legacy: false, locale, messages: { en: { admin: resources }, zh: { admin: zhResources } } })] },
})

describe('recent pass-rate card summary', () => {
  it('keeps the configured window, counts and collection progress visible before opening details', () => {
    const wrapper = mountRecentRate(evidence())
    expect(wrapper.get('[data-recent-pass-rate-counts]').text()).toBe('Last 20 checks within 36 hours · 8 passed · 66.7%')
    expect(wrapper.get('[data-recent-pass-rate-state]').text()).toBe('Collected 12/20 checks')
    expect(wrapper.get('[data-recent-pass-rate-state]').classes()).toContain('text-amber-800')
    expect(wrapper.get('details > summary').text()).toBe('Evidence and rule details')
    expect(wrapper.get('details').attributes('open')).toBeUndefined()
    expect(wrapper.get('[data-recent-pass-rate-details]').text()).not.toContain('Last 20 checks')
    expect(wrapper.get('[data-recent-pass-rate-details]').text()).toContain('below 80% passing')
    expect(wrapper.get('[data-recent-pass-rate-details]').text()).toContain('Model: test-model · Rule version: v1')
    wrapper.unmount()
  })

  it.each([undefined, null])('labels unavailable evidence (%s) without inventing a percentage', value => {
    const wrapper = mountRecentRate(value)
    expect(wrapper.get('[data-recent-pass-rate-unavailable]').text()).toBe('Recent pass-rate statistics unavailable')
    expect(wrapper.find('[data-recent-pass-rate-counts]').exists()).toBe(false)
    expect(wrapper.find('details').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('0%')
    wrapper.unmount()
  })

  it('preserves stale and collecting explanations when the current window has no valid samples', () => {
    const wrapper = mountRecentRate(evidence({
      samples: 0, passed: 0, pass_rate: null, stale: true, blocked: true, reason: 'stale_evidence',
    }))
    expect(wrapper.get('[data-recent-pass-rate-counts]').text()).toBe('Last 20 checks within 36 hours · 0 passed · —')
    expect(wrapper.get('[data-recent-pass-rate-state]').text()).toBe('Calls paused')
    expect(wrapper.get('[data-recent-pass-rate-state]').classes()).toContain('text-red-800')
    expect(wrapper.get('[data-recent-pass-rate-progress]').text()).toBe('Collected 0/20 checks')
    expect(wrapper.get('[data-pass-rate-stale]').text()).toContain('expiry does not remove an existing hold')
    expect(wrapper.get('[data-pass-rate-recovery-requirement]').text()).toContain('20 valid checks, at least 90% passing')
    wrapper.unmount()
  })

  it('keeps stale evidence yellow and a missing percentage neutral without claiming health', () => {
    const stale = mountRecentRate(evidence({ ready: true, samples: 20, stale: true }))
    expect(stale.get('[data-recent-pass-rate-state]').text()).toBe('Stale evidence')
    expect(stale.get('[data-recent-pass-rate-state]').classes()).toContain('text-amber-800')
    stale.unmount()
    const unknown = mountRecentRate(evidence({ ready: true, samples: 20, pass_rate: null, reason: '' }))
    expect(unknown.get('[data-recent-pass-rate-state]').text()).toBe('Unknown')
    expect(unknown.get('[data-recent-pass-rate-state]').classes()).toContain('text-gray-600')
    expect(unknown.text()).not.toContain('Window complete')
    expect(unknown.get('[data-recent-pass-rate-counts]').text()).toContain('· —')
    unknown.unmount()
  })

  it.each([
    [3600, '1 hour'],
    [5400, '90 minutes'],
    [604800, '7 days'],
  ])('shows an exact readable unit for a %s-second window', (window_seconds, label) => {
    const wrapper = mountRecentRate(evidence({ window_seconds, ready: true, samples: 20 }))
    expect(wrapper.get('[data-recent-pass-rate-counts]').text()).toContain(`within ${label}`)
    expect(wrapper.get('[data-recent-pass-rate-state]').text()).toBe('Window complete')
    wrapper.unmount()
  })

  it('renders the requested Chinese summary and a visible partial sample count', () => {
    const wrapper = mountRecentRate(evidence({ window_seconds: 604800, window_size: 10, samples: 6, passed: 5, pass_rate: 83.333 }), 'zh')
    expect(wrapper.get('[data-recent-pass-rate-counts]').text()).toBe('7 天内最近 10 次 · 通过 5 次 · 83.3%')
    expect(wrapper.get('[data-recent-pass-rate-state]').text()).toBe('已采集 6/10 次')
    wrapper.unmount()
  })
})
