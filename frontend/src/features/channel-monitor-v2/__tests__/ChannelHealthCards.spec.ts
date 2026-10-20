import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { afterEach, describe, expect, it, vi } from 'vitest'
import ChannelHealthCards from '../ChannelHealthCards.vue'
import { cardHealth, hasMonitorTraffic, modelsForCard, recentRequestsForCard, requestHealth, timelineForCard } from '../monitorCards'
import zh from '@/i18n/locales/zh/channelMonitorV2'
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
  it('shows called models in the matching platform/group despite redacted request counts', () => {
    const called = { ...row, model: 'called-model' }
    const inactive = { ...row, model: 'unused-model', has_traffic: false }
    const otherGroup = { ...called, group_id: 8 }
    const otherPlatform = { ...called, platform: 'grok' }
    expect(modelsForCard(row, [called, inactive, otherGroup, otherPlatform])).toEqual([called])
    expect(hasMonitorTraffic(called)).toBe(true)
    expect(hasMonitorTraffic(inactive)).toBe(false)
    expect(modelsForCard({ ...row, model: 'different' }, [called])).toEqual([])
  })

  it('keeps sparse and ignored-error-only traffic without inventing health', () => {
    const sparse = { ...row, model: 'sparse-model', health: { ...health, overall: 'unknown' as const } }
    expect(modelsForCard(row, [sparse])).toEqual([sparse])
    expect(cardHealth(sparse.health, 'overall')).toBe('unknown')
    expect(cardHealth(health, 'ttft')).toBe('warning')
  })

  it('aligns intervals over the selected range, including gaps and aggregation lag', () => {
    const slots = timelineForCard(row, coverage)
    expect(slots).toHaveLength(18)
    expect(slots[0].bucket).toBeUndefined()
    expect(slots[2].bucket).toEqual(row.buckets[0])
    expect(slots[17].bucket).toBeUndefined()
    expect(slots[17].end).toBe(Date.parse(coverage.requested_end!))
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

  it('renders channel cards, a real 20-request strip, and only active models without a quality-test section', async () => {
    vi.useFakeTimers()
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [{ ...row, recent_requests: [request('success'), request('slow', 1), request('error', 2), request('ignored', 3)] }],
        modelRows: [{ ...row, model: 'called-model' }, { ...row, model: 'unused-model', has_traffic: false }],
        coverage, healthMode: 'overall', nextRefreshAt: Date.now() + 60000, refreshing: false },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    expect(wrapper.findAll('[data-testid=channel-health-card]')).toHaveLength(1)
    expect(wrapper.findAll('[data-testid=model-health]')).toHaveLength(1)
    expect(wrapper.text()).toContain('called-model')
    expect(wrapper.text()).not.toContain('unused-model')
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
    expect(wrapper.findAll('[data-testid=model-health] [data-state=warning]')).toHaveLength(1)
    wrapper.unmount()
    expect(vi.getTimerCount()).toBe(0)
  })
})
