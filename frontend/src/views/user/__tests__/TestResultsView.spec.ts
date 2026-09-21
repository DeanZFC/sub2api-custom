import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestResultsView from '../TestResultsView.vue'

const api = vi.hoisted(() => ({ list: vi.fn(), history: vi.fn(), error: vi.fn() }))
vi.mock('@/api/testResults', () => ({ testResultsAPI: { list: api.list, history: api.history } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: api.error }) }))

const baseResult = { plan_id: 10, test_definition_id: 1, test_name: 'Pelican', group_id: 8, group_name: 'Group Eight', account_id: 3, model_id: 'gpt-6-astra', output_kind: 'html', status: 'success', created_at: '2026-09-15T12:00:00Z' }
const mountResults = () => mount(TestResultsView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
  stubs: {
    AppLayout: { template: '<main><slot /></main>' }, Icon: true,
    TestResultOutput: { props: ['result', 'compact'], template: '<div data-output :data-result-id="result.id" :data-compact="compact" />' },
    BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><button data-close @click="$emit(\'close\')">Close</button><slot /></section>' },
  },
} })

beforeEach(() => {
  vi.resetAllMocks()
  api.history.mockResolvedValue({ items: [] })
})
afterEach(() => vi.useRealTimers())

describe('channel quality result groups', () => {
  it('uses ordered group tabs and combines multiple test types under one account', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, plan_order: 10 },
      { ...baseResult, id: 2, plan_id: 20, test_definition_id: 2, test_name: 'Candy', output_kind: 'number', output_numeric: 29, plan_order: 10 },
      { ...baseResult, id: 3, group_id: 9, group_name: 'Next Group', account_id: 4, plan_order: 20 },
    ])
    const wrapper = mountResults()
    await flushPromises()

    expect(api.list).toHaveBeenCalledWith(3)
    expect(api.history).not.toHaveBeenCalled()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Group Eight', 'Next Group'])
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.account #3')
    expect(wrapper.get('[data-numeric-test]').text()).toContain('Candy')
    expect(wrapper.get('[data-numeric-test] strong').text()).toBe('29')
    expect(wrapper.get('[data-content-test]').text()).toContain('Pelican')
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.account #4')
    expect(wrapper.find('[data-numeric-test]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('hides account identifiers for group-only checks even when a legacy response has an account ID', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, target_mode: 'group', account_id: 99 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.get('[data-account-result] h3').text()).toBe('tests.groupCheck')
    expect(wrapper.text()).not.toContain('#99')
    wrapper.unmount()
  })

  it('keeps groups stable while polling and supports keyboard group navigation', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const first = { ...baseResult, id: 1, group_name: 'Alpha', group_id: 1, plan_order: 0 }
    const second = { ...baseResult, id: 2, group_name: 'Beta', group_id: 2, plan_order: 0 }
    api.list.mockResolvedValueOnce([second, first]).mockResolvedValueOnce([{ ...first, id: 4 }, { ...second, id: 3 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Alpha', 'Beta'])
    await wrapper.findAll('[role="tab"]')[0].trigger('keydown', { key: 'ArrowRight' })
    expect(wrapper.findAll('[role="tab"]')[1].attributes('aria-selected')).toBe('true')
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.text())).toEqual(['Alpha', 'Beta'])
    expect(wrapper.findAll('[role="tab"]')[1].attributes('aria-selected')).toBe('true')
    wrapper.unmount()
  })

  it('preserves document scrolling while polling and leaves model filters accessible without matches', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, group_name: 'A', model_id: 'model-a' },
      { ...baseResult, id: 2, group_id: 9, group_name: 'B', model_id: 'model-b' },
    ])
    const wrapper = mountResults()
    await flushPromises()
    const panel = wrapper.get<HTMLElement>('[role="tabpanel"]')
    expect(panel.classes()).not.toContain('overflow-y-auto')
    panel.element.scrollTop = 150
    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(panel.element.scrollTop).toBe(150)
    await wrapper.get('select').setValue('model-b')
    expect(panel.element.scrollTop).toBe(0)
    expect(panel.text()).toContain('tests.noMatches')
    expect(wrapper.find('select').exists()).toBe(true)
    await wrapper.findAll('[role="tab"]')[1].trigger('click')
    expect(panel.findAll('[data-account-result]')).toHaveLength(1)
    wrapper.unmount()
  })
})

describe('channel quality result series', () => {
  it('shows only the three newest outputs and fetches complete history on demand', async () => {
    const results = [1, 2, 3, 4, 5].map(id => ({ ...baseResult, id, created_at: `2026-09-15T${10 + id}:00:00Z` }))
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['5', '4', '3'])
    expect(wrapper.findAll('[data-result-gallery] [data-output]').every(output => output.attributes('data-compact') === '')).toBe(true)
    await wrapper.get('[data-content-test] button').trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenCalledWith(5, undefined)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['5', '4', '3', '2', '1'])
    wrapper.unmount()
  })

  it('uses stable type IDs and keeps different model and reasoning configurations separate', async () => {
    const results = [
      { ...baseResult, id: 4, test_name: 'Renamed Pelican', reasoning_effort: 'ultra', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 3, reasoning_effort: 'ultra', created_at: '2026-09-15T13:00:00Z' },
      { ...baseResult, id: 2, reasoning_effort: 'high' },
      { ...baseResult, id: 1, reasoning_effort: 'ultra', model_id: 'another-model' },
    ]
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results.slice(0, 2) })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    const series = wrapper.findAll('[data-content-test]')
    expect(series).toHaveLength(3)
    const renamed = series.find(item => item.text().includes('Renamed Pelican'))!
    expect(renamed.text()).toContain('gpt-6-astra · tests.reasoningEffort: ultra')
    expect(renamed.findAll('[data-output]').map(output => output.attributes('data-result-id'))).toEqual(['4', '3'])
    await renamed.get('button').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]')).toHaveLength(2)
    wrapper.unmount()
  })

  it('uses new execution time when a previous record is retried', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 2, started_at: '2026-09-15T12:00:00Z' },
      { ...baseResult, id: 1, created_at: '2026-09-15T10:00:00Z', started_at: '2026-09-15T13:00:00Z' },
    ])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['1', '2'])
    expect(wrapper.findAll('figcaption')[0].text()).toContain(new Date('2026-09-15T13:00:00Z').toLocaleString())
    wrapper.unmount()
  })

  it('orders each account test type using its configured order', async () => {
    api.list.mockResolvedValue([
      { ...baseResult, id: 1, test_definition_id: 1, test_name: 'Alpha', test_order: 20 },
      { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Beta', test_order: 0 },
    ])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-content-test] h4').map(heading => heading.text())).toEqual(['Beta', 'Alpha'])
    wrapper.unmount()
  })

  it('keeps previous successful outputs and removes failed records from cards and history', async () => {
    const results = [
      { ...baseResult, id: 3, status: 'failed', error_message: 'private upstream failure', created_at: '2026-09-15T14:00:00Z' },
      { ...baseResult, id: 2, status: 'success' },
      { ...baseResult, id: 1, status: 'passed' },
    ]
    api.list.mockResolvedValue(results)
    api.history.mockResolvedValue({ items: results })
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-result-gallery] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['2', '1'])
    await wrapper.get('[data-content-test] button').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]').map(output => output.attributes('data-result-id'))).toEqual(['2', '1'])
    expect(wrapper.text()).not.toContain('private')
    wrapper.unmount()
  })

  it('shows an empty state if every result failed', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, status: 'failed' }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.text()).toContain('tests.empty')
    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each(['pending', 'running'])('keeps an initial %s check visible', async status => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1, status }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.findAll('[data-account-result]')).toHaveLength(1)
    expect(wrapper.text()).toContain('tests.running')
    expect(wrapper.find('[data-content-test] button').exists()).toBe(false)
    expect(api.history).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it.each(['number', 'text'])('shows three recent %s results newest first', async outputKind => {
    api.list.mockResolvedValue([1, 2, 3, 4].map(id => ({ ...baseResult, id, output_kind: outputKind, output_numeric: id })))
    const wrapper = mountResults()
    await flushPromises()
    if (outputKind === 'number') {
      expect(wrapper.findAll('[data-numeric-gallery] strong').map(item => item.text())).toEqual(['4', '3', '2'])
    } else {
      expect(wrapper.findAll('[data-result-gallery] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['4', '3', '2'])
    }
    wrapper.unmount()
  })

  it('labels completed HTML tests as completed without a manual review state', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 1 }])
    const wrapper = mountResults()
    await flushPromises()
    expect(wrapper.get('[data-content-test] .badge').text()).toBe('tests.completed')
    expect(wrapper.get('[data-content-test] .badge').classes()).toContain('badge-success')
    expect(wrapper.text()).not.toContain('tests.awaitingReview')
    wrapper.unmount()
  })

  it('pages history and retries a failed page without discarding earlier results', async () => {
    api.list.mockResolvedValue([{ ...baseResult, id: 5 }])
    api.history.mockResolvedValueOnce({ items: [{ ...baseResult, id: 5 }], next_before_id: 5 })
      .mockRejectedValueOnce(new Error('history unavailable'))
      .mockResolvedValueOnce({ items: [{ ...baseResult, id: 4 }, { ...baseResult, id: 3 }] })
    const wrapper = mountResults()
    await flushPromises()
    await wrapper.get('[data-content-test] button').trigger('click')
    await flushPromises()
    await wrapper.findAll('[data-dialog] button').find(button => button.text() === 'tests.loadMore')!.trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenLastCalledWith(5, 5)
    expect(wrapper.find('[data-dialog] [role="alert"]').exists()).toBe(true)
    expect(wrapper.findAll('[data-dialog] [data-output]')).toHaveLength(1)
    await wrapper.findAll('[data-dialog] button').find(button => button.text() === 'common.retry')!.trigger('click')
    await flushPromises()
    expect(api.history).toHaveBeenLastCalledWith(5, 5)
    expect(wrapper.findAll('[data-dialog] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['5', '4', '3'])
    expect(wrapper.find('[data-dialog] [role="alert"]').exists()).toBe(false)
    expect(wrapper.findAll('[data-dialog] button').some(button => button.text() === 'tests.loadMore')).toBe(false)
    wrapper.unmount()
  })

  it('ignores a slow history response after opening a different test', async () => {
    const first = { ...baseResult, id: 1, test_name: 'Alpha' }
    const second = { ...baseResult, id: 2, test_definition_id: 2, test_name: 'Beta' }
    api.list.mockResolvedValue([first, second])
    let resolveFirst!: (value: unknown) => void
    api.history.mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
      .mockResolvedValueOnce({ items: [second] })
    const wrapper = mountResults()
    await flushPromises()
    const buttons = wrapper.findAll('[data-content-test] button')
    await buttons[0].trigger('click')
    expect(wrapper.find('[data-dialog] [role="status"]').exists()).toBe(true)
    await wrapper.get('[data-close]').trigger('click')
    await buttons[1].trigger('click')
    await flushPromises()
    resolveFirst({ items: [first], next_before_id: 1 })
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] [data-output]').map(item => item.attributes('data-result-id'))).toEqual(['2'])
    expect(wrapper.find('[data-dialog] [role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
