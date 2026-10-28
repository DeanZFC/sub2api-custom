import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import ChannelQualityScoreEvidence from '../ChannelQualityScoreEvidence.vue'
import resources from '@/i18n/locales/en/admin/resources'
import type { ChannelQualityScore } from '@/api/admin/channelQuality'
const score = (): ChannelQualityScore => ({
  value: 80, status: 'partial', samples: 40, models: 1, latest_at: '2026-10-27T10:00:00Z', model: 'model-a', window_seconds: 900, max_requests: 100, min_samples: 20, confidence: 0.75, coverage_percent: 75,
  dimensions: [
    { key: 'error_rate', value: 0, score: 100, weight: 40, samples: 40, status: 'ready', unit: 'percent', reason: '' },
    { key: 'cache_rate', value: null, score: null, weight: 25, samples: 0, status: 'unknown', unit: 'percent', reason: 'no_eligible_samples' },
  ],
})
const render = (data: ChannelQualityScore) => mount(ChannelQualityScoreEvidence, { props: { score: data }, global: { plugins: [createI18n({ legacy: false, locale: 'en', messages: { en: { admin: resources } } })] } })
describe('quality score evidence', () => {
  it('explains partial evidence and keeps missing cache values unknown', () => {
    const wrapper = render(score())
    expect(wrapper.text()).toContain('Partial evidence')
    expect(wrapper.text()).toContain('Dimension coverage 75%')
    expect(wrapper.text()).toContain('Model: model-a')
    expect(wrapper.get('[data-score-dimension="cache_rate"]').text()).toContain('—')
    expect(wrapper.get('[data-score-dimension="cache_rate"]').text()).toContain('No eligible samples')
    expect(wrapper.get('[data-score-dimension="error_rate"]').text()).toContain('0%')
  })
  it('uses token-per-second units for the independent output-speed dimension', () => {
    const data = score()
    data.dimensions.push({ key: 'tps', value: 42.5, score: 85, weight: 10, samples: 30, status: 'ready', unit: 'token/s', reason: '' })
    const wrapper = render(data)
    const metric = wrapper.get('[data-score-dimension="tps"]')
    expect(metric.text()).toContain('42.5 token/s')
    expect(metric.text()).not.toContain('42.5 ms')
  })

  it.each(['insufficient', 'incomplete', 'disabled'] as const)('does not manufacture a zero score for %s', status => {
    const wrapper = render({ ...score(), status, value: null })
    expect(wrapper.get('strong').text()).toBe('—')
    expect(wrapper.get('[data-score-status]').text()).toBe(resources.tests.scoring.status[status])
  })
})
