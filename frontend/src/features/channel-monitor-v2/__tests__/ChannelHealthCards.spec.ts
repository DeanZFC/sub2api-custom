import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import ChannelHealthCards from '../ChannelHealthCards.vue'
import { cardHealth, hasMonitorTraffic, historyBucketsForCard, recentRequestsForCard, requestHealth } from '../monitorCards'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import type { TestResult } from '@/types'
import type { MonitorCoverage, MonitorHealth, MonitorMatrixRow, MonitorMetric, MonitorRecentRequest } from '@/api/channelMonitorV2'

const metrics: MonitorMetric = {
  success_requests: 0, error_requests: 0, request_count: 0, token_count: 0, rpm: 0, tpm: 0,
  has_cache_samples: true, error_rate: 0.04, cache_rate: 0.7, cache_rate_numerator: 0, cache_rate_denominator: 0,
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
const requestTime = (time: string) => new Intl.DateTimeFormat('zh', {
  month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', second: '2-digit',
}).format(new Date(time))

describe('channel health cards', () => {
  it('keeps passive health and traffic when counts are redacted', () => {
    expect(hasMonitorTraffic(row)).toBe(true)
    expect(hasMonitorTraffic({ ...row, has_traffic: false })).toBe(false)
    expect(cardHealth(health, 'ttft')).toBe('warning')
  })

  it.each([
    ['90m', 90 * 60, 300, 2],
    ['24h', 24 * 3600, 3600, 0],
    ['7d', 7 * 86400, 12 * 3600, 6],
    ['30d', 30 * 86400, 86400, 0],
  ] as const)('shows 20 slots with real aggregate intervals for %s', (_, seconds, bucketSeconds, padding) => {
    const end = Date.parse('2026-10-22T00:00:00Z')
    const selected = { ...coverage, requested_start: new Date(end - seconds * 1000).toISOString(), requested_end: new Date(end).toISOString(), bucket_seconds: bucketSeconds }
    const slots = historyBucketsForCard([], selected)
    expect(slots).toHaveLength(20)
    expect(slots.filter(slot => slot === null)).toHaveLength(padding)
    expect(slots[padding]?.start).toBe(end - Math.min(20, seconds / bucketSeconds) * bucketSeconds * 1000)
    expect(slots[19]?.end).toBe(end)
    expect(slots[19]!.end - slots[19]!.start).toBe(bucketSeconds * 1000)
  })

  it('preserves gaps and the selected range during partial aggregation, regardless of bucket order', () => {
    const lastBucket = { ...row.buckets[0], bucket_start: '2026-10-20T01:20:00Z' }
    const outside = { ...row.buckets[0], bucket_start: coverage.requested_end! }
    const slots = historyBucketsForCard([outside, lastBucket, row.buckets[0]], coverage)
    expect(slots[4]?.bucket).toBe(row.buckets[0])
    expect(slots[5]?.bucket).toBeUndefined()
    expect(slots[18]?.bucket).toBe(lastBucket)
    expect(slots[19]).toEqual({ start: Date.parse(coverage.data_through), end: Date.parse(coverage.requested_end!), bucket: undefined })
    expect(slots.flatMap(slot => slot?.bucket ? [slot.bucket] : [])).toHaveLength(2)
    expect(historyBucketsForCard([], { ...coverage, requested_end: undefined })[19]?.end).toBe(Date.parse(coverage.data_through))
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

  it.each([['success', 'healthy'], ['slow', 'healthy'], ['error', 'critical'], ['ignored', 'critical']] as const)('maps %s requests to %s independently of time-bucket health', (status, state) => {
    expect(requestHealth(request(status))).toBe(state)
    expect(requestHealth(null)).toBe('unknown')
  })

  it('renders compact channel cards and real request states without a per-model status section', async () => {
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [{ ...row, recent_requests: [request('success'), request('slow', 1), request('error', 2), request('ignored', 3)] }],
        coverage, groupBy: 'platform_group', healthMode: 'overall' },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    expect(wrapper.findAll('[data-testid=channel-health-card]')).toHaveLength(1)
    expect(wrapper.findAll('[data-testid=model-health]')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('模型状态')
    expect(wrapper.get('[data-testid=channel-section-title]').text()).toContain('OpenAI')
    expect(wrapper.get('[data-testid=channel-section-title]').text()).toContain('1')
    expect(wrapper.text()).not.toContain('降智状态')
    expect(wrapper.text()).toContain('被动健康')
    const requests = wrapper.get('[data-testid=recent-requests]').findAll('button[data-state]')
    expect(requests).toHaveLength(20)
    expect(requests.slice(-4).map(slot => slot.attributes('data-state'))).toEqual(['healthy', 'healthy', 'critical', 'critical'])
    expect(requests[0].attributes('data-state')).toBe('unknown')
    expect(wrapper.get('[data-testid=recent-requests]').text()).toContain('绿：成功（含响应较慢） · 红：失败（含已忽略错误） · 灰：暂无请求')
    expect(wrapper.get('[data-testid=latest-request-time]').text()).toBe(`最近请求：${requestTime(request('ignored', 3).occurred_at)}`)
    await wrapper.setProps({ healthMode: 'ttft' })
    expect(requests.slice(-4).map(slot => slot.attributes('data-state'))).toEqual(['healthy', 'healthy', 'critical', 'critical'])
    wrapper.unmount()
  })

  it('renders interval statistics above requests with the original score colors and mode-specific metrics', async () => {
    const scoredHealth = { ...health, score: 96, ttft_score: 65, error_rate_score: 100, cache_score: 30, cache: 'critical' as const }
    const wrapper = mount(ChannelHealthCards, {
      props: { coverage, rows: [{ ...row, buckets: [{ ...row.buckets[0], health: scoredHealth }], recent_requests: [request('success')] }], groupBy: 'platform_group', healthMode: 'overall' },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    const history = wrapper.get('[data-testid=health-history]')
    expect(history.text()).toContain('近 20 个时段统计')
    expect(history.text()).toContain('综合 · 5 分钟粒度')
    expect(wrapper.html().indexOf('data-testid="health-history"')).toBeLessThan(wrapper.html().indexOf('data-testid="recent-requests"'))
    const slots = history.findAll('button[data-state]')
    expect(slots).toHaveLength(20)
    expect(slots[0].attributes('aria-label')).toBe('当前时间范围内暂无更多统计时段')
    expect(slots[3].attributes('aria-label')).toContain('该时段暂无统计数据')
    expect(slots[4].get('span').classes()).toContain('health-score10')
    expect(slots[4].attributes('aria-label')).toContain('健康分 96\n成功率 96.0%\n首 Token AVG 9.0s · P50 8.0s · P95 12.0s\n缓存率 70.0%\n请求时长 -')
    expect(slots[4].attributes('title')).toBeUndefined()
    for (const [healthMode, color] of [['ttft', 'health-score7'], ['cache', 'health-score3'], ['success', 'health-score10']] as const) {
      await wrapper.setProps({ healthMode })
      expect(slots[4].get('span').classes()).toContain(color)
      expect(wrapper.get('[data-testid=recent-requests]').findAll('button[data-state]')[19].attributes('data-state')).toBe('healthy')
    }
    const unknownHealth: MonitorHealth = { ...health, overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', cache: 'unknown' }
    await wrapper.setProps({ rows: [{ ...row, buckets: [{ ...row.buckets[0], metrics: { ...metrics, has_cache_samples: false }, health: unknownHealth }] }] })
    expect(slots[4].get('span').classes()).toContain('health-unknown')
    expect(slots[4].attributes('aria-label')).toContain('成功率 -')
    expect(slots[4].attributes('aria-label')).toContain('缓存率 -')
    wrapper.unmount()
  })

  it('shows the latest actual request per card and complete hover metrics, preserving zero and missing values', async () => {
    const latest = { ...request('success', 10), ttft_ms: 800, cache_rate: 0.8, duration_ms: 3500 }
    const zero = { ...request('success', 1), ttft_ms: 0, cache_rate: 0, duration_ms: 0 }
    const failed = { ...request('error'), error_category: 'upstream_5xx' }
    const wrapper = mount(ChannelHealthCards, {
      props: {
        rows: [{ ...row, recent_requests: [latest, failed, zero] }, { ...row, group_id: 8, recent_requests: [] }],
        coverage, groupBy: 'platform_group', healthMode: 'overall',
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    expect(wrapper.findAll('[data-testid=latest-request-time]').map(el => el.text())).toEqual([
      `最近请求：${requestTime(latest.occurred_at)}`, '最近请求：-',
    ])
    const slots = wrapper.get('[data-testid=recent-requests]').findAll('button[data-state]')
    expect(slots[19].attributes('aria-label')).toBe([
      'gpt-test · 请求成功', `请求时间：${requestTime(latest.occurred_at)}`, '首字：800ms', '缓存率 80.0%', '总耗时：3.5s',
    ].join('\n'))
    expect(slots[19].attributes('title')).toBeUndefined()
    expect(slots[18].attributes('aria-label')).toContain('首字：0ms\n缓存率 0.00%\n总耗时：0ms')
    expect(slots[17].attributes('aria-label')).toContain('首字：-\n缓存率 -\n总耗时：-')
    expect(slots[17].attributes('aria-label')).not.toContain('上游 5xx')
    expect(slots[0].attributes('aria-label')).toBe('当前时间范围内暂无更多请求')
    await wrapper.setProps({ rows: [{ ...row, recent_requests: [request('error', 20)] }] })
    expect(wrapper.get('[data-testid=latest-request-time]').text()).toBe(`最近请求：${requestTime(request('error', 20).occurred_at)}`)
    wrapper.unmount()
  })

  it.each(['error', 'ignored'] as const)('shows admin diagnostics and opens the exact %s log from the tooltip', async (status) => {
    const failed = { ...request(status), error_category: 'internal', error: {
      log_id: 42, status_code: 500, upstream_status_code: 502, error_type: 'upstream_error',
      message: 'Upstream worker failed: trace=test-trace',
    } }
    const wrapper = mount(ChannelHealthCards, {
      attachTo: document.body,
      props: { rows: [{ ...row, recent_requests: [failed] }], coverage, groupBy: 'platform_group', healthMode: 'overall', admin: true },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    try {
      const trigger = wrapper.get('[data-testid=recent-requests]').findAll('button[data-state]')[19]
      await trigger.trigger('click')
      const tooltip = document.querySelector<HTMLElement>('[role=tooltip]')!
      expect(tooltip.textContent).toContain('内部错误\nHTTP 500\n上游 502\n错误类型：upstream_error\n具体错误：Upstream worker failed: trace=test-trace')
      const action = tooltip.querySelector('button')!
      expect(action.textContent).toBe('查看完整错误日志')
      action.click()
      expect(wrapper.emitted('open-error')).toEqual([[42]])
      await wrapper.setProps({ admin: false })
      expect(trigger.attributes('aria-label')).not.toContain('内部错误')
      expect(trigger.attributes('aria-label')).not.toContain('错误类型')
      expect(trigger.attributes('aria-label')).not.toContain('具体错误')
      expect(trigger.attributes('aria-label')).not.toContain('test-trace')
      expect(trigger.attributes('aria-label')).not.toContain('HTTP 500')
      await trigger.trigger('click')
      expect(document.querySelector('[role=tooltip]')?.textContent).not.toContain('内部错误')
      expect(document.querySelector('[role=tooltip] button')).toBeNull()
      expect(wrapper.emitted('open-error')).toEqual([[42]])
    } finally {
      wrapper.unmount()
    }
  })

  it('keeps a log entry accessible when the recorded error message is empty', async () => {
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [{ ...row, recent_requests: [{ ...request('error'), error_category: 'internal', error: { log_id: 43 } }] }],
        coverage, groupBy: 'platform_group', healthMode: 'overall', admin: true },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    expect(wrapper.get('[data-testid=recent-requests]').findAll('button[data-state]')[19].attributes('aria-label')).toContain('具体错误：无错误消息')
    wrapper.unmount()
  })

  it('groups actual called models with newer versions first and no unused model placeholders', async () => {
    const wrapper = mount(ChannelHealthCards, {
      props: {
        rows: [
          { ...row, model: 'gpt-5.3' }, { ...row, model: 'gpt-5.4' },
          { ...row, group_id: 8, model: 'gpt-5.4' },
          { ...row, model: 'unused-model', has_traffic: false },
        ],
        coverage, groupBy: 'platform_group_model', healthMode: 'overall',
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
      props: { coverage, rows: [{ ...row, model: 'model-a' }], groupBy: 'platform_group_model', healthMode: 'overall', testResults: tests },
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

it('shows a dash for sync-only traffic and zero for measured cache misses after redaction', async () => {
  const wrapper = mount(ChannelHealthCards, {
    props: { rows: [{ ...row, metrics: { ...metrics, cache_rate: 0, has_cache_samples: false } }], coverage, groupBy: 'platform_group', healthMode: 'cache' },
    global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
  })
  expect(wrapper.findAll('dd')[0].text()).toBe('-')
  await wrapper.setProps({ rows: [{ ...row, metrics: { ...metrics, cache_rate: 0, has_cache_samples: true } }] })
  expect(wrapper.findAll('dd')[0].text()).toBe('0.00%')
})
