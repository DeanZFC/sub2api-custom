import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import { createI18n } from 'vue-i18n'
import zhAdmin from '@/i18n/locales/zh/admin/resources'
import TestExecutionUsage from '../TestExecutionUsage.vue'
import type { TestExecutionInfo } from '@/types'

const usage = (extra: Partial<TestExecutionInfo> = {}): TestExecutionInfo => ({
  input_tokens: null, output_tokens: 0, reasoning_tokens: null,
  cache_read_tokens: null, cache_creation_tokens: null, attempts: [], ...extra,
})
const render = (info?: TestExecutionInfo | null) => mount(TestExecutionUsage, {
  props: { info },
  global: { plugins: [createI18n({ legacy: false, locale: 'zh', messages: { zh: { admin: zhAdmin } } })] },
})

describe('test execution usage', () => {
  it('keeps legacy results without usage hidden and distinguishes unknown from zero', () => {
    const legacy = render()
    expect(legacy.find('[data-test-usage]').exists()).toBe(false)
    const wrapper = render(usage())
    expect(wrapper.get('summary').text()).toContain('输入 未知 / 输出 0')
    expect(wrapper.get('summary').text()).toContain('0 次请求')
    expect(wrapper.text()).toContain('上游未返回的用量显示未知')
  })

  it('shows cooldown and individual requests so failed spending is inspectable', () => {
    const wrapper = render(usage({
      input_tokens: 30, output_tokens: 10, reasoning_tokens: 4,
      next_check_at: '2026-10-16T10:00:00Z', consecutive_failures: 3,
      attempts: [
        { ...usage(), input_tokens: 10, output_tokens: 2, reasoning_tokens: 1, started_at: '2026-10-16T09:00:00Z', http_status: 503, failure_kind: 'server' },
        { ...usage(), input_tokens: 20, output_tokens: 8, reasoning_tokens: 3, started_at: '2026-10-16T09:00:01Z', http_status: 200 },
      ],
    }))
    expect(wrapper.get('[data-next-check]').text()).toContain('连续异常 3 次')
    expect(wrapper.get('summary').text()).toContain('2 次请求')
    expect(wrapper.get('summary').text()).toContain('输入 30 / 输出 10')
    expect(wrapper.findAll('[data-usage-attempt]')).toHaveLength(2)
    expect(wrapper.findAll('[data-usage-attempt]')[0].text()).toContain('HTTP 503')
    expect(wrapper.findAll('[data-usage-attempt]')[0].text()).toContain('输入 10 / 输出 2')
  })
})
