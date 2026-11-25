import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import { describe, expect, it } from 'vitest'
import ChannelHealthCards from '../ChannelHealthCards.vue'
import { cardHealth, hasMonitorTraffic, historyBucketsForCard } from '../monitorCards'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import type { TestResult } from '@/types'
import type { MonitorCoverage, MonitorHealth, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'

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
describe('channel health cards', () => {
  it('shows each group’s own multiplier, including free groups, without inventing a rate for platform totals', () => {
    const wrapper = mount(ChannelHealthCards, {
      props: {
        rows: [
          { ...row, group_rate_multiplier: 0.035 },
          { ...row, group_id: 8, group_name: 'Free', group_rate_multiplier: 0 },
          { ...row, group_id: 9, group_name: 'Unknown rate' },
          { ...row, group_id: undefined, group_name: undefined },
        ],
        coverage, groupBy: 'platform_group', healthMode: 'overall',
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    const cards = wrapper.findAll('[data-testid=channel-health-card]')
    expect(cards[0].get('[data-testid=group-rate-multiplier]').text()).toBe('倍率 0.035×')
    expect(cards[1].get('[data-testid=group-rate-multiplier]').text()).toBe('倍率 0×')
    expect(cards[2].find('[data-testid=group-rate-multiplier]').exists()).toBe(false)
    expect(cards[3].find('[data-testid=group-rate-multiplier]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('shows each channel’s own model metrics and switches details without changing overall metrics', async () => {
    const modelRows: MonitorMatrixRow[] = [
      { ...row, model: 'gpt-6.1', metrics: { ...metrics, cache_rate: 0.9 }, buckets: [] },
      { ...row, model: 'gpt-6', metrics: { ...metrics, cache_rate: 0, has_cache_samples: true } },
      { ...row, model: 'gpt-5', metrics: { ...metrics, cache_rate: 0, has_cache_samples: false } },
      { ...row, group_id: 8, model: 'gpt-6.1', metrics: { ...metrics, cache_rate: 0.5 } },
      { ...row, platform: 'anthropic', model: 'wrong-platform' },
      { ...row, model: 'unused', has_traffic: false },
    ]
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [row, { ...row, group_id: 8 }], modelRows,
        coverage, groupBy: 'platform_group', healthMode: 'overall' },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    const cards = wrapper.findAll('[data-testid=channel-health-card]')
    expect(cards.map(card => card.findAll('[data-testid=model-metrics-card]').length)).toEqual([3, 1])
    expect(wrapper.findAll('[data-testid=model-metrics-toggle]')).toHaveLength(0)
    const modelCells = cards[0].findAll('[data-testid=model-metrics-card]')
    expect(modelCells.map(model => model.get('[data-testid=model-cache-rate]').text())).toEqual(['90.0%', '0.00%', '-'])
    expect(cards[1].get('[data-testid=model-metrics-card]').text()).toContain('50.0%')
    expect(wrapper.text()).not.toContain('wrong-platform')
    expect(wrapper.text()).not.toContain('unused')

    await modelCells[0].trigger('click')
    expect(cards[0].get('[data-testid=card-detail-scope]').text()).toContain('gpt-6.1')
    expect(cards[0].findAll('dd')[0].text()).toBe('70.0%')
    expect(cards[0].get('[data-testid=health-history]').findAll('button[data-state]')[4].attributes('aria-label')).toContain('该时段暂无统计数据')
    expect(cards[1].find('[data-testid=card-detail-scope]').exists()).toBe(false)
    await cards[0].get('[data-testid=card-detail-scope] button').trigger('click')
    expect(cards[0].get('[data-testid=health-history]').findAll('button[data-state]')[4].attributes('aria-label')).toContain('缓存率 70.0%')

    await modelCells[0].trigger('click')
    await wrapper.setProps({ modelRows: modelRows.slice(1) })
    expect(cards[0].find('[data-testid=card-detail-scope]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('expands models per channel and keeps selected details when the model is collapsed', async () => {
    const models = ['gpt-6.1', 'gpt-6', 'gpt-5', 'gpt-4']
    const modelRows: MonitorMatrixRow[] = [7, 8].flatMap(group_id => models.map(model => ({
      ...row, group_id, model, buckets: model === 'gpt-4' ? [] : row.buckets,
    })))
    const wrapper = mount(ChannelHealthCards, {
      props: {
        rows: [row, { ...row, group_id: 8 }],
        modelRows, coverage, groupBy: 'platform_group', healthMode: 'overall',
      },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    const cards = wrapper.findAll('[data-testid=channel-health-card]')
    expect(cards.map(card => card.findAll('[data-testid=model-metrics-card]').length)).toEqual([3, 3])
    expect(cards.map(card => card.get('[data-testid=model-metrics-toggle]').attributes('aria-expanded'))).toEqual(['false', 'false'])

    await cards[0].get('[data-testid=model-metrics-toggle]').trigger('click')
    expect(cards.map(card => card.findAll('[data-testid=model-metrics-card]').length)).toEqual([4, 3])
    expect(cards[0].get('[data-testid=model-metrics-toggle]').attributes('aria-expanded')).toBe('true')
    const hiddenModel = cards[0].findAll('[data-testid=model-metrics-card]').find(model => model.text().includes('gpt-4'))!
    await hiddenModel.trigger('click')
    expect(hiddenModel.attributes('aria-pressed')).toBe('true')
    expect(cards[0].get('[data-testid=card-detail-scope]').text()).toContain('gpt-4')
    expect(cards[0].get('[data-testid=health-history]').findAll('button[data-state]')[4].attributes('aria-label')).toContain('该时段暂无统计数据')

    await cards[0].get('[data-testid=model-metrics-toggle]').trigger('click')
    expect(cards[0].findAll('[data-testid=model-metrics-card]')).toHaveLength(3)
    expect(cards[0].get('[data-testid=model-metrics-toggle]').attributes('aria-expanded')).toBe('false')
    expect(cards[0].get('[data-testid=card-detail-scope]').text()).toContain('gpt-4')
    expect(cards[0].get('[data-testid=health-history]').findAll('button[data-state]')[4].attributes('aria-label')).toContain('该时段暂无统计数据')
    expect(cards[1].find('[data-testid=card-detail-scope]').exists()).toBe(false)
    wrapper.unmount()
  })

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

  it('shows interval history as the only status strip even when the response includes recent requests', () => {
    const wrapper = mount(ChannelHealthCards, {
      props: { rows: [{ ...row, recent_requests: [{ model: 'gpt-test', status: 'error', occurred_at: coverage.data_through }] }],
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
    expect(wrapper.find('[data-testid=recent-requests]').exists()).toBe(false)
    expect(wrapper.findAll('button[data-state]')).toHaveLength(20)
    wrapper.unmount()
  })

  it('renders interval statistics with the original score colors and mode-specific metrics', async () => {
    const scoredHealth = { ...health, score: 96, ttft_score: 65, error_rate_score: 100, cache_score: 30, cache: 'critical' as const }
    const wrapper = mount(ChannelHealthCards, {
      props: { coverage, rows: [{ ...row, buckets: [{ ...row.buckets[0], health: scoredHealth }] }], groupBy: 'platform_group', healthMode: 'overall' },
      global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { ProviderIcon: true } },
    })
    const history = wrapper.get('[data-testid=health-history]')
    expect(history.text()).toContain('近 20 个时段统计')
    expect(history.text()).toContain('综合 · 5 分钟粒度')
    const slots = history.findAll('button[data-state]')
    expect(slots).toHaveLength(20)
    expect(slots[0].attributes('aria-label')).toBe('当前时间范围内暂无更多统计时段')
    expect(slots[3].attributes('aria-label')).toContain('该时段暂无统计数据')
    expect(slots[4].get('span').classes()).toContain('health-score10')
    expect(slots[4].attributes('aria-label')).toContain('健康分 96\n成功率 96.0%\n首 Token AVG 9.0s · P50 8.0s · P95 12.0s\n输出 TPS —\n缓存率 70.0%\n请求时长 -')
    expect(slots[4].attributes('title')).toBeUndefined()
    for (const [healthMode, color] of [['ttft', 'health-score7'], ['cache', 'health-score3'], ['success', 'health-score10']] as const) {
      await wrapper.setProps({ healthMode })
      expect(slots[4].get('span').classes()).toContain(color)
    }
    const unknownHealth: MonitorHealth = { ...health, overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', cache: 'unknown' }
    await wrapper.setProps({ rows: [{ ...row, buckets: [{ ...row.buckets[0], metrics: { ...metrics, has_cache_samples: false }, health: unknownHealth }] }] })
    expect(slots[4].get('span').classes()).toContain('health-unknown')
    expect(slots[4].attributes('aria-label')).toContain('成功率 -')
    expect(slots[4].attributes('aria-label')).toContain('缓存率 -')
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
