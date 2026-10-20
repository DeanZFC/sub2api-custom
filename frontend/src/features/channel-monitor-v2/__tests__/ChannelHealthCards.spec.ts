import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ChannelHealthCards from '../ChannelHealthCards.vue'
import { cardHealth, hasMonitorTraffic, recentRequestsForCard, requestHealth } from '../monitorCards'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import type { TestResult } from '@/types'
import type { MonitorCoverage, MonitorHealth, MonitorMatrixRow, MonitorMetric, MonitorRecentRequest } from '@/api/channelMonitorV2'

const metrics: MonitorMetric = {
  success_requests: 0, error_requests: 0, request_count: 0, token_count: 0, rpm: 0, tpm: 0,
  error_rate: 0.04, cache_rate: 0.7, cache_rate_numerator: 0, cache_rate_denominator: 0,
  ttft: { sample_count: 0, p50_ms: 8000, p95_ms: 12000, avg_ms: 9000 },
  duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
}
const health: MonitorHealth = { overall: 'healthy', error_rate: 'healthy', ttft: 'warning', cache: 'healthy', minimum_sample: 20 }
const coverage: MonitorCoverage = {
  requested_start: '2026-10-20T00:00:00Z', requested_end: '2026-10-20T01:30:00Z',
  coverage_start: '2026-10-20T00:10:00Z', data_through: '2026-10-20T01:25:00Z',
  computed_at: '2026-10-20T01:26:00Z', aggregation_lag_seconds: 60, coverage_complete: false, bucket_seconds: 300,
}
const row: MonitorMatrixRow = {
  platform: 'openai', group_id: 7, group_name: 'GPT Plus', metrics, health, has_traffic: true,
  buckets: [{ bucket_start: '2026-10-20T00:10:00Z', metrics, health }],
}
const request = (status: MonitorRecentRequest['status'], seconds = 0): MonitorRecentRequest => ({
  occurred_at: new Date(Date.parse(coverage.data_through) + seconds * 1000).toISOString(), model: 'gpt-test', status,
})
afterEach(() => vi.useRealTimers())

describe('channel health cards', () => {
  it('keeps passive health and traffic when counts are redacted', () => {
    expect(hasMonitorTraffic(row)).toBe(true)
    expect(hasMonitorTraffic({ ...row, has_traffic: false })).toBe(false)
    expect(cardHealth(health, 'ttft')).toBe('warning')
  })

  it('sorts and caps actual requests at 20, with gray placeholders before sparse history', () => {
    const requests = Array.from({ length: 25 }, (_, index) => request('success', index)).reverse()
    const slots = recentRequestsForCard(requests)
    expect(slots).toHaveLength(20)
    expect(slots[0]).toEqual(request('success', 5))
    expect(slots[19]).toEqual(request('success', 24))
    expect(recentRequestsForCard([request('error')]).filter(slot => slot === null)).toHaveLength(19)
    expect(recentRequestsForCard()).toEqual(Array(20).fill(null))
    expect(requests[0]).toEqual(request('success', 24))
  })

  it.each([['success', 'healthy'], ['slow', 'warning'], ['error', 'critical'], ['ignored', 'warning']] as const)('maps %s requests to %s independently of time-bucket health', (status, state) => {
    expect(requestHealth(request(status))).toBe(state)
    expect(requestHealth(null)).toBe('unknown')
  })

  it('renders compact channel cards and real request states without a per-model status section', async () => {
    vi.useFakeTimers()
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [{ ...row, recent_requests: [request('success'), request('slow', 1), request('error', 2), request('ignored', 3)] }],
        groupBy: 'platform_group', healthMode: 'overall', nextRefreshAt: Date.now() + 60000, refreshing: false },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    expect(wrapper.findAll('[data-testid=channel-health-card]')).toHaveLength(1)
    expect(wrapper.findAll('[data-testid=model-health]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('模型状态')
    expect(wrapper.get('[data-testid=channel-section-title]').text()).toContain('OpenAI')
    expect(wrapper.get('[data-testid=channel-section-title]').text()).toContain('1')
    expect(wrapper.text()).not.toContain('降智状态')
    expect(wrapper.text()).toContain('被动健康')
    const requests = wrapper.get('[data-testid=recent-requests]').findAll('[role=img]')
    expect(requests).toHaveLength(20)
    expect(requests.slice(-4).map(slot => slot.attributes('data-state'))).toEqual(['healthy', 'warning', 'critical', 'warning'])
    expect(requests[0].attributes('data-state')).toBe('unknown')
    expect(wrapper.text()).toContain('60s 后刷新')
    await vi.advanceTimersByTimeAsync(1000)
    expect(wrapper.text()).toContain('59s 后刷新')
    await wrapper.setProps({ healthMode: 'ttft' })
    expect(requests.slice(-4).map(slot => slot.attributes('data-state'))).toEqual(['healthy', 'warning', 'critical', 'warning'])
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })

  it('groups actual called models with newer versions first and no unused model placeholders', async () => {
    const wrapper = mount(ChannelHealthCards, {
      props: {
        rows: [
          { ...row, model: 'gpt-5.3' }, { ...row, model: 'gpt-5.4' },
          { ...row, group_id: 8, model: 'gpt-5.4' },
          { ...row, model: 'unused-model', has_traffic: false },
        ],
        groupBy: 'platform_group_model', healthMode: 'overall', nextRefreshAt: Date.now() + 60000, refreshing: false,
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    expect(wrapper.findAll('[data-testid=channel-section-title]').map(el => el.text())).toEqual(['gpt-5.42', 'gpt-5.31'])
    expect(wrapper.findAll('[data-testid=channel-health-card]')).toHaveLength(3)
    expect(wrapper.text()).not.toContain('unused-model')
    expect(wrapper.text()).not.toContain('其他模型')
    await wrapper.setProps({ groupBy: 'platform_group', rows: [row, { ...row, group_id: 8 }] })
    expect(wrapper.findAll('[data-testid=channel-section-title]')).toHaveLength(1)
    expect(wrapper.get('[data-testid=channel-section-title]').text()).toContain('OpenAI')
    wrapper.unmount()
  })
  it('opens all group accounts from a model card without mixing other groups', async () => {
    const tests: TestResult[] = [
      { id: 1, group_id: 7, account_id: 10, model_id: 'model-a', test_key: 'pelican', output_kind: 'html', status: 'success', output_html: '<svg />' },
      { id: 2, group_id: 7, account_id: 11, model_id: 'model-b', test_key: 'pelican', output_kind: 'html', status: 'success', output_html: '<svg />' },
      { id: 3, group_id: 8, account_id: 12, model_id: 'private-model', test_key: 'pelican', output_kind: 'html', status: 'success', output_html: '<svg />' },
    ]
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [{ ...row, model: 'model-a' }], groupBy: 'platform_group_model', healthMode: 'overall', nextRefreshAt: Date.now() + 60000, refreshing: false, testResults: tests },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: {
        ProviderIcon: true, TestResultOutput: true,
        BaseDialog: { props: ['show'], template: '<div v-if="show"><slot /></div>' },
      } },
    })
    await wrapper.get('[data-testid=pelican-gallery-open]').trigger('click')
    expect(wrapper.findAll('[data-testid=pelican-account]')).toHaveLength(2)
    expect(wrapper.get('[data-testid=pelican-accounts]').text()).toContain('model-b')
    expect(wrapper.get('[data-testid=pelican-accounts]').text()).not.toContain('private-model')
    wrapper.unmount()
  })

})
