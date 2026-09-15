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

  it('keeps test tabs as names only and hides account details for group-only results', async () => {
    const groupResult = {
      id: 10,
      plan_id: 20,
      test_name: 'Pelican',
      group_id: 8,
      group_name: 'Group Eight',
      account_id: null,
      model_id: 'gpt-6-astra',
      output_kind: 'text',
      status: 'success',
      created_at: '2026-09-15T12:00:00Z',
    }
    api.list.mockResolvedValue([
      groupResult,
      { ...groupResult, id: 11, plan_id: 21, test_name: 'Number Check', created_at: '2026-09-15T11:00:00Z' },
    ])
    const wrapper = mount(TestResultsView, { global: {
      plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, TestResultOutput: true, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><slot /></section>' } },
    } })
    await flushPromises()

    const tabButtons = wrapper.findAll('button').filter(button => button.classes('shrink-0'))
    expect(tabButtons.map(button => button.text())).toEqual(['Pelican', 'Number Check'])
    expect(tabButtons.every(button => !button.text().match(/\d/))).toBe(true)
    expect(wrapper.findAll('article')[0].text()).toContain('Group Eight')
    expect(wrapper.findAll('article')[0].text()).not.toContain('Account #')
    wrapper.unmount()
  })

  it('shows an older result retried successfully as the latest by its new execution time', async () => {
    const result = { plan_id: 10, test_name: 'Pelican', group_id: 8, group_name: 'Group Eight', account_id: 3, model_id: 'gpt-6-astra', output_kind: 'text', status: 'success' }
    const retryStartedAt = '2026-09-15T13:00:00Z'
    const previousStartedAt = '2026-09-15T12:00:00Z'
    api.list.mockResolvedValue([
      { ...result, id: 2, created_at: previousStartedAt, started_at: previousStartedAt, finished_at: '2026-09-15T12:05:00Z' },
      { ...result, id: 1, created_at: '2026-09-15T10:00:00Z', started_at: retryStartedAt, finished_at: '2026-09-15T13:05:00Z' },
    ])
    const wrapper = mount(TestResultsView, { global: {
      plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, TestResultOutput: { props: ['result'], template: '<div data-output :data-result-id="result.id" />' }, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><slot /></section>' } },
    } })
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get('article [data-output]').attributes('data-result-id')).toBe('1')
    expect(wrapper.get('article').text()).toContain(new Date(retryStartedAt).toLocaleString())
    expect(wrapper.get('article').text()).not.toContain(new Date('2026-09-15T10:00:00Z').toLocaleString())

    await wrapper.get('article').trigger('click')
    const history = wrapper.get('[data-dialog]').findAll('article')
    expect(history.map(card => card.get('[data-output]').attributes('data-result-id'))).toEqual(['1', '2'])
    expect(history[0].text()).toContain(new Date(retryStartedAt).toLocaleString())
    expect(history[1].text()).toContain(new Date(previousStartedAt).toLocaleString())
    wrapper.unmount()
  })
})
