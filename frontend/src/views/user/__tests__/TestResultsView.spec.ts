import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestResultsView from '../TestResultsView.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), error: vi.fn() }))
vi.mock('@/api/testResults', () => ({ testResultsAPI: { list: api.list } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.error }) }))

beforeEach(() => vi.clearAllMocks())
afterEach(() => vi.useRealTimers())

describe('test result reasoning effort', () => {
  it('shows each execution effort on the latest result and its history', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const result = { plan_id: 10, test_name: 'Pelican', group_id: 8, group_name: 'Group Eight', account_id: 3, model_id: 'gpt-6-astra', output_kind: 'text', status: 'success' }
    api.list.mockResolvedValue([
      { ...result, id: 3, reasoning_effort: 'ultra', created_at: '2026-09-15T12:00:00Z' },
      { ...result, id: 2, reasoning_effort: 'high', created_at: '2026-09-15T11:00:00Z' },
      { ...result, id: 1, created_at: '2026-09-15T10:00:00Z' },
    ])
    const wrapper = mount(TestResultsView, { global: {
      plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, TestResultOutput: true, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><slot /></section>' } },
    } })
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get('article').text()).toContain('gpt-6-astra · tests.reasoningEffort: ultra')
    await wrapper.get('article').trigger('click')
    const history = wrapper.get('[data-dialog]').findAll('article')
    expect(history).toHaveLength(3)
    expect(history[0].text()).toContain('gpt-6-astra · tests.reasoningEffort: ultra')
    expect(history[1].text()).toContain('gpt-6-astra · tests.reasoningEffort: high')
    expect(history[2].text()).not.toContain('tests.reasoningEffort')
    wrapper.unmount()
  })
})
