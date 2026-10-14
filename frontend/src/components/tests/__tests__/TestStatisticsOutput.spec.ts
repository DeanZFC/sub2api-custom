import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestStatisticsOutput from '../TestStatisticsOutput.vue'
import type { TestOutputStatistics } from '@/types'

describe('statistics recovery evidence', () => {
  it('shows probe evidence without turning missing usage into measured statistics', async () => {
    const statistics: TestOutputStatistics = {
      window_start: '2026-10-14T05:00:00Z', window_end: '2026-10-14T06:00:00Z',
      total_requests: 0, success_requests: 0, failed_requests: 0, success_rate: null,
      cache_rate: null, avg_first_token_ms: null, first_token_samples: 0,
      cache_read_tokens: 0, cache_input_tokens: 0,
    }
    const wrapper = mount(TestStatisticsOutput, {
      props: { statistics },
      global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })] },
    })
    expect(wrapper.find('[data-statistics-recovery-probe]').exists()).toBe(false)
    for (const status of ['success', 'failed'] as const) {
      await wrapper.setProps({ statistics: { ...statistics, recovery_probe: {
        status, started_at: '2026-10-14T06:00:00Z', finished_at: '2026-10-14T06:00:01Z', latency_ms: 1000,
      } } })
      expect(wrapper.get('[data-statistics-recovery-probe]').text()).toContain(status === 'success' ? 'tests.statisticsProbeSuccess' : 'tests.statisticsProbeFailed')
      expect(wrapper.get('[data-success-rate]').text()).toBe('—')
      expect(wrapper.get('[data-cache-rate]').text()).toBe('—')
      expect(wrapper.get('[data-first-token]').text()).toBe('—')
      expect(wrapper.findAll('[data-request-outcome]')).toHaveLength(0)
    }
    wrapper.unmount()
  })
})
