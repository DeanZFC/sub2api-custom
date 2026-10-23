import { mount } from '@vue/test-utils'
import { expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import MonitorTrendChart from '../MonitorTrendChart.vue'
import type { MonitorMetric, MonitorHealth } from '@/api/channelMonitorV2'
import zh from '@/i18n/locales/zh/channelMonitorV2'

vi.mock('vue-chartjs', () => ({ Line: { props: ['data', 'options'], template: '<div class="chart-data">{{ JSON.stringify(data) }}</div>' } }))

it('keeps cache gaps and measured zero without smoothing them into neighboring buckets', () => {
  const metric: MonitorMetric = {
    request_count: 10, success_requests: 10, error_requests: 0, token_count: 10000,
    rpm: 10, tpm: 10000, error_rate: 0, cache_rate: 0.9,
    cache_rate_numerator: 0, cache_rate_denominator: 0, has_cache_samples: true,
    ttft: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
    duration: { sample_count: 0, p50_ms: null, p95_ms: null, avg_ms: null },
  }
  const health: MonitorHealth = { overall: 'unknown', error_rate: 'unknown', ttft: 'unknown', cache: 'unknown', minimum_sample: 20 }
  const wrapper = mount(MonitorTrendChart, {
    props: { coverage: null, trend: [
      { bucket_start: '2026-10-23T00:00:00Z', metrics: metric, health },
      { bucket_start: '2026-10-23T00:01:00Z', metrics: { ...metric, cache_rate: 0, has_cache_samples: false }, health },
      { bucket_start: '2026-10-23T00:02:00Z', metrics: { ...metric, cache_rate: 0 }, health },
    ] },
    global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh } })], stubs: { Icon: true, EmptyState: true } },
  })
  const data = JSON.parse(wrapper.get('.chart-data').text())
  expect(data.datasets[1].data).toEqual([90, null, 0])
})
