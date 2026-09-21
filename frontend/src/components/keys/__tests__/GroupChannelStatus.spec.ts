import { mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import type { MonitorCoverage, MonitorHealth, MonitorMatrixBucket, MonitorMatrixRow, MonitorMetric } from '@/api/channelMonitorV2'
import GroupChannelStatus from '../GroupChannelStatus.vue'

const coverage: MonitorCoverage = {
  requested_start: '2026-09-21T00:00:00Z',
  requested_end: '2026-09-21T01:30:00Z',
  coverage_start: '2026-09-21T00:00:00Z',
  data_through: '2026-09-21T01:30:00Z',
  computed_at: '2026-09-21T01:30:00Z',
  aggregation_lag_seconds: 0,
  coverage_complete: true,
  bucket_seconds: 60,
}

function metrics(requestCount = 0): MonitorMetric {
  return {
    success_requests: requestCount, error_requests: 0, request_count: requestCount, token_count: 0,
    rpm: 0, tpm: 0, error_rate: 0, cache_rate: 0, cache_rate_numerator: 0, cache_rate_denominator: 0,
    ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  }
}

function health(score: number | null = 100, state: MonitorHealth['overall'] = 'healthy'): MonitorHealth {
  return { score, overall: state, error_rate: state, ttft: 'unknown', minimum_sample: 20 }
}

function bucket(minute: number, score: number | null = 100, state: MonitorHealth['overall'] = 'healthy', requestCount = 0): MonitorMatrixBucket {
  return {
    bucket_start: new Date(Date.parse(coverage.requested_start) + minute * 60000).toISOString(),
    metrics: metrics(requestCount),
    health: health(score, state),
  }
}

function row(buckets: MonitorMatrixBucket[], platform = 'openai'): MonitorMatrixRow {
  return { platform, group_id: 7, group_name: 'Group', metrics: metrics(), health: health(), buckets }
}

const messages: Record<string, string> = {
  'common.loading': 'Loading...',
  'common.noData': 'No data',
  'keys.groupChannelStatus.recent': 'Recent channel health; newest interval on the right',
  'keys.groupChannelStatus.composite': 'Each interval shows the worst known health across platforms',
  'keys.groupChannelStatus.unavailable': 'Status unavailable',
  'channelMonitorV2.matrix.noTrafficAt': '{time} · No traffic',
  'channelMonitorV2.matrix.scoreLine': 'Health score {score}',
  'channelMonitorV2.matrix.healthyLegend': 'Healthy',
  'channelMonitorV2.matrix.warningLegend': 'Warning',
  'channelMonitorV2.matrix.criticalLegend': 'Critical',
  'channelMonitorV2.matrix.unknownLegend': 'Unknown',
}

vi.mock('vue-i18n', async (importOriginal) => ({
  ...await importOriginal<typeof import('vue-i18n')>(),
  useI18n: () => ({
    t: (key: string, params?: Record<string, unknown>) => (messages[key] ?? key).replace(/\{(\w+)\}/g, (_, name) => String(params?.[name] ?? '')),
    locale: { value: 'en' },
  }),
}))

function render(props: {
  rows: MonitorMatrixRow[]
  coverage?: MonitorCoverage | null
  loading?: boolean
  unavailable?: boolean
}) {
  return mount(GroupChannelStatus, {
    props: { coverage, ...props },
  })
}

describe('GroupChannelStatus', () => {
  it('aligns the last ten intervals, keeps gaps gray and puts the newest status on the right', () => {
    const wrapper = render({ rows: [row([bucket(2), bucket(80, 90), bucket(89, 15, 'critical')])] })
    const cells = wrapper.findAll('[data-status-cell]')
    expect(cells).toHaveLength(10)
    expect(cells[0].attributes('data-bucket-start')).toBe('2026-09-21T01:20:00.000Z')
    expect(cells[9].attributes('data-bucket-start')).toBe('2026-09-21T01:29:00.000Z')
    expect(cells[0].classes()).toContain('health-score9')
    expect(cells[9].classes()).toContain('health-score2')
    for (const cell of cells.slice(1, 9)) expect(cell.classes()).toContain('health-unknown')
    expect(cells[9].attributes('title')).toContain('Health score 15')
    expect(wrapper.attributes('aria-label')).toContain('newest interval on the right')
    expect(wrapper.find('[data-group-channel-status]').exists()).toBe(true)
  })

  it('uses known scores when privacy has redacted all request counts to zero', () => {
    const wrapper = render({ rows: [row([bucket(89, 52)])] })
    expect(wrapper.findAll('[data-status-cell]')[9].classes()).toContain('health-score5')
    expect(wrapper.text()).not.toContain('No data')
    expect(wrapper.html()).not.toContain('request_count')
    expect(wrapper.html()).not.toContain('success_requests')
  })

  it('does not backfill the current interval with an old green summary or hide coverage gaps', () => {
    const wrapper = render({
      rows: [row([bucket(85, 100)])],
      coverage: { ...coverage, coverage_complete: false, data_through: '2026-09-21T01:26:00Z' },
    })
    const cells = wrapper.findAll('[data-status-cell]')
    expect(cells[5].classes()).toContain('health-score10')
    expect(cells[9].classes()).toContain('health-unknown')
    expect(cells.filter((cell) => cell.classes().includes('health-unknown'))).toHaveLength(9)
  })

  it('combines all platforms conservatively for each interval, independently of row ordering', () => {
    const openai = row([bucket(87, 100), bucket(88, 12, 'critical'), bucket(89, 99)])
    const anthropic = row([bucket(87, 40, 'warning'), bucket(88, 100), bucket(89, null, 'critical', 40)], 'anthropic')
    const unknown = row([bucket(87, null, 'unknown'), bucket(88, null, 'unknown')], 'gemini')
    const forward = render({ rows: [openai, anthropic, unknown] })
    const reverse = render({ rows: [unknown, anthropic, openai] })
    const colors = (wrapper: ReturnType<typeof render>) => wrapper.findAll('[data-status-cell]').map((cell) => cell.classes().find((name) => name.startsWith('health-')))
    expect(colors(forward)).toEqual(colors(reverse))
    expect(colors(forward).slice(-3)).toEqual(['health-score4', 'health-score1', 'health-critical'])
    expect(forward.attributes('aria-label')).toContain('worst known health across platforms')
  })

  it('uses the original coarse health fallback and stays gray when neither score nor traffic is known', () => {
    const wrapper = render({ rows: [row([
      bucket(87, null, 'warning', 30),
      bucket(88, null, 'healthy', 30),
      bucket(89, null, 'healthy', 0),
    ])] })
    const cells = wrapper.findAll('[data-status-cell]')
    expect(cells[7].classes()).toContain('health-warning')
    expect(cells[8].classes()).toContain('health-healthy')
    expect(cells[9].classes()).toContain('health-unknown')
  })

  it.each([
    { name: 'no rows', props: { rows: [] }, label: 'No data' },
    { name: 'missing coverage', props: { rows: [row([bucket(89)])], coverage: null }, label: 'No data' },
    { name: 'loading', props: { rows: [row([bucket(89)])], loading: true }, label: 'Loading...' },
    { name: 'unavailable', props: { rows: [row([bucket(89)])], unavailable: true }, label: 'Status unavailable' },
    { name: 'invalid coverage', props: { rows: [row([bucket(89)])], coverage: { ...coverage, requested_start: 'invalid' } }, label: 'No data' },
  ])('shows a neutral track and label for $name', ({ props, label }) => {
    const wrapper = render(props)
    expect(wrapper.findAll('[data-status-cell]')).toHaveLength(10)
    expect(wrapper.findAll('[data-status-cell]').every((cell) => cell.classes().includes('health-unknown'))).toBe(true)
    expect(wrapper.text()).toBe(label)
  })

  it('supports older coverage without requested_end and normalizes bucket timezone offsets', () => {
    const recent = bucket(89, 70)
    recent.bucket_start = '2026-09-21T09:29:00+08:00'
    const wrapper = render({ rows: [row([recent])], coverage: { ...coverage, requested_end: undefined } })
    expect(wrapper.findAll('[data-status-cell]')[9].classes()).toContain('health-score7')
  })

  it('lets a cell click reach its parent option without adding buttons or focus stops', async () => {
    const onClick = vi.fn()
    const wrapper = mount({
      components: { GroupChannelStatus },
      template: '<div @click="onClick"><GroupChannelStatus :rows="rows" :coverage="coverage" /></div>',
      setup: () => ({ onClick, rows: [row([bucket(89)])], coverage }),
    })
    await wrapper.find('[data-status-cell]').trigger('click')
    expect(onClick).toHaveBeenCalledOnce()
    expect(wrapper.find('button, [role="button"], [tabindex]').exists()).toBe(false)
  })
})
