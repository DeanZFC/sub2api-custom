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
    expect(tabButtons.map(button => button.text())).toEqual(['Number Check', 'Pelican'])
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

  it('keeps filters accessible on no matches and preserves scroll during polling', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const result = { plan_id: 10, test_name: 'Numbers', output_kind: 'number', status: 'success', output_numeric: 29 }
    api.list.mockResolvedValue([
      { ...result, id: 1, group_id: 8, group_name: 'Group A', account_id: 3, model_id: 'model-a' },
      { ...result, id: 2, group_id: 9, group_name: 'Group B', account_id: 4, model_id: 'model-b' },
    ])
    const wrapper = mount(TestResultsView, { global: {
      plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
      stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, TestResultOutput: true, BaseDialog: true },
    } })
    await flushPromises()
    const panel = wrapper.get<HTMLElement>('section[tabindex="0"]')
    // Filters and results share the document scroll. The page must not create
    // a second nested scrolling viewport that traps either filter column.
    expect(panel.classes()).not.toContain('overflow-y-auto')
    expect(wrapper.get('aside nav').classes()).not.toContain('overflow-y-auto')
    expect(wrapper.get('aside').classes()).not.toContain('max-h-[30dvh]')
    expect(panel.findAll('article')).toHaveLength(1)
    expect(wrapper.findAll('aside nav button')[0].classes()).toContain('font-semibold')
    panel.element.scrollTop = 150
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(panel.element.scrollTop).toBe(150)

    await wrapper.get('aside select').setValue('model-b')
    expect(panel.element.scrollTop).toBe(0)
    expect(panel.text()).toContain('tests.noMatches')
    expect(wrapper.find('aside select').exists()).toBe(true)
    await wrapper.get('aside select').setValue('')
    expect(panel.findAll('article')).toHaveLength(1)
    panel.element.scrollTop = 180
    await wrapper.findAll('aside nav button')[1].trigger('click')
    expect(panel.element.scrollTop).toBe(0)
    expect(panel.text()).toContain('Group B')
    wrapper.unmount()
  })
})

describe('user test result visibility', () => {
  const result = { plan_id: 10, test_name: 'Pelican', group_id: 8, group_name: 'Group Eight', account_id: 3, model_id: 'gpt-6-astra', output_kind: 'text' }
  const mountResults = () => mount(TestResultsView, { global: {
    plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
    stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, TestResultOutput: { props: ['result'], template: '<div data-output :data-result-id="result.id" />' }, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><slot /></section>' } },
  } })

  it('shows the empty state when a target has only failed results', async () => {
    api.list.mockResolvedValue([
      { ...result, id: 2, status: 'failed', error_message: 'private upstream failure', response_text: 'private failed output' },
      { ...result, id: 1, status: 'error', error_message: 'private upstream error' },
    ])
    const wrapper = mountResults()
    await flushPromises()

    expect(wrapper.text()).toContain('tests.empty')
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(wrapper.text()).not.toContain('private')
    expect(wrapper.text()).not.toContain('Pelican')
    wrapper.unmount()
  })

  it.each([0, 10])('orders test type tabs with administrator configured sort order %i', async order => {
    api.list.mockResolvedValue([
      { ...result, id: 2, test_definition_id: 2, test_name: 'Candy', test_order: 20, status: 'success' },
      { ...result, id: 1, test_definition_id: 1, test_name: 'Pelican', test_order: order, status: 'success' },
    ])
    const wrapper = mountResults()
    await flushPromises()

    const tabs = wrapper.findAll('section.card button')
    expect(tabs.map(tab => tab.text())).toEqual(['Pelican', 'Candy'])
    wrapper.unmount()
  })

  it('orders group filters and result sections by the configured test rule order', async () => {
    api.list.mockResolvedValue([
      { ...result, id: 2, group_id: 2, group_name: 'Second', plan_order: 20, test_name: 'Check', status: 'success' },
      { ...result, id: 1, group_id: 1, group_name: 'First', plan_order: 10, test_name: 'Check', status: 'success' },
    ])
    const wrapper = mountResults()
    await flushPromises()

    expect(wrapper.findAll('aside nav button').map(button => button.text())).toEqual(['First', 'Second'])
    wrapper.unmount()
  })

  it('keeps equal-order tabs stable when polling changes the latest plan id', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const alpha = { ...result, test_name: 'Alpha', test_order: 0, status: 'success' }
    const beta = { ...result, test_name: 'Beta', test_order: 0, status: 'success' }
    api.list
      .mockResolvedValueOnce([
        { ...alpha, id: 1, plan_id: 100, created_at: '2026-09-15T12:00:00Z' },
        { ...beta, id: 2, plan_id: 1, created_at: '2026-09-15T11:00:00Z' },
      ])
      .mockResolvedValueOnce([
        { ...alpha, id: 3, plan_id: 2, created_at: '2026-09-15T13:00:00Z' },
        { ...beta, id: 4, plan_id: 200, created_at: '2026-09-15T10:00:00Z' },
      ])
    const wrapper = mountResults()
    await flushPromises()
    const tabTexts = () => wrapper.findAll('section.card button').map(tab => tab.text())
    expect(tabTexts()).toEqual(['Alpha', 'Beta'])

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(tabTexts()).toEqual(['Alpha', 'Beta'])
    wrapper.unmount()
  })

  it('keeps the previous success visible and omits failures from history', async () => {
    api.list.mockResolvedValue([
      { ...result, id: 4, status: 'failed', error_message: 'private upstream failure', created_at: '2026-09-15T14:00:00Z' },
      { ...result, id: 3, status: 'success', created_at: '2026-09-15T13:00:00Z' },
      { ...result, id: 2, status: 'error', error_message: 'private upstream error', created_at: '2026-09-15T12:00:00Z' },
      { ...result, id: 1, status: 'passed', created_at: '2026-09-15T11:00:00Z' },
    ])
    const wrapper = mountResults()
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get('article [data-output]').attributes('data-result-id')).toBe('3')
    await wrapper.get('article').trigger('click')
    const history = wrapper.get('[data-dialog]').findAll('article')
    expect(history.map(card => card.get('[data-output]').attributes('data-result-id'))).toEqual(['3', '1'])
    expect(wrapper.text()).not.toContain('private')
    wrapper.unmount()
  })

  it.each(['pending', 'running'])('preserves the initial %s result', async status => {
    api.list.mockResolvedValue([{ ...result, id: 1, status, created_at: '2026-09-15T12:00:00Z' }])
    const wrapper = mountResults()
    await flushPromises()

    expect(wrapper.findAll('article')).toHaveLength(1)
    expect(wrapper.get('article').text()).toContain(status)
    wrapper.unmount()
  })
})
