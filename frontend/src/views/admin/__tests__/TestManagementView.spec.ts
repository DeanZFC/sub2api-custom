import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'

const api = vi.hoisted(() => ({
  listTypes: vi.fn(), listPlans: vi.fn(), createType: vi.fn(), updateType: vi.fn(), deleteType: vi.fn(),
  createPlan: vi.fn(), updatePlan: vi.fn(), deletePlan: vi.fn(), runPlan: vi.fn(), listResults: vi.fn(),
  retryResult: vi.fn(), getGroups: vi.fn(), getAccounts: vi.fn(), success: vi.fn(), error: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { tests: api, groups: { getAll: api.getGroups }, accounts: { list: api.getAccounts } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))

const plan = { id: 10, name: 'Candy hourly', test_definition_id: 2, group_id: 8, model_id: 'test-model', cron_expression: '0 * * * *', enabled: true, max_results: 50 }
const makeWrapper = () => mount(TestManagementView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: {} } })],
  stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h3>{{ title }}</h3><slot /><slot name="footer" /></section>' } }
} })

beforeEach(() => {
  vi.clearAllMocks()
  api.listTypes.mockResolvedValue([{ id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true }])
  api.listPlans.mockResolvedValue([plan])
  api.getGroups.mockResolvedValue([{ id: 8, name: 'Group Eight' }])
  api.getAccounts.mockResolvedValue({ items: [{ id: 3, name: 'Account Three', group_ids: [8] }, { id: 4, name: 'Other Group Account', group_ids: [9] }], pages: 1, total: 2 })
  api.listResults.mockResolvedValue([])
  api.runPlan.mockResolvedValue(undefined)
  api.retryResult.mockResolvedValue(undefined)
})
afterEach(() => vi.useRealTimers())

describe('configurable test management', () => {
  it('saves a new type with its chosen output format and prompt', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text().includes('common.create'))!.trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const inputs = dialog.findAll('input')
    await inputs[0].setValue('Pelican')
    await inputs[1].setValue('pelican-custom')
    await inputs[2].setValue('html')
    await dialog.get('textarea').setValue('Draw a pelican riding a motorcycle in HTML.')
    await dialog.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.createType).toHaveBeenCalledWith(expect.objectContaining({ name: 'Pelican', key: 'pelican-custom', output_kind: 'html', prompt: 'Draw a pelican riding a motorcycle in HTML.' }))
    wrapper.unmount()
  })

  it('selects an account within a group while preserving the group target', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    const row = wrapper.findAll('tbody tr')[0]
    await row.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const accountSelect = dialog.findAll('select').find(s => s.find('option[value="3"]').exists())!
    expect(accountSelect.find('option[value="4"]').exists()).toBe(false)
    await accountSelect.setValue('3')
    await dialog.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ group_id: 8, account_id: 3, model_id: 'test-model', cron_expression: '0 * * * *' }))
    wrapper.unmount()
  })

  it('refreshes retained results after asynchronous execution without a page reload', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.listPlans.mockResolvedValue([{ ...plan, reasoning_effort: 'ultra' }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.tests.run')!.trigger('click')
    await flushPromises()
    expect(api.runPlan).toHaveBeenCalledWith(10)
    expect(wrapper.get('[data-dialog]').text()).toContain('admin.tests.reasoningEffort: ultra')
    api.listResults.mockResolvedValue([{ id: 101, plan_id: 10, model_id: 'test-model', reasoning_effort: 'ultra', status: 'success', output_kind: 'number', output_numeric: 29, response_text: '最终答案：29' }])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-dialog]').text()).toContain('29')
    expect(wrapper.get('[data-dialog]').text()).toContain('admin.tests.reasoningEffort: ultra')
    expect(api.listResults).toHaveBeenLastCalledWith(10, 50)
    wrapper.unmount()
    const count = api.listResults.mock.calls.length
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.listResults).toHaveBeenCalledTimes(count)
  })

  it('replaces a failed result in place when retrying the same account', async () => {
    const failed = {
      id: 101,
      plan_id: 10,
      account_id: 3,
      model_id: 'test-model',
      status: 'failed',
      output_kind: 'number',
      error_message: 'upstream failed',
      response_text: 'old output',
    }
    const running = {
      ...failed,
      status: 'running',
      error_message: null,
      response_text: null,
      output_numeric: null,
      latency_ms: null,
    }
    api.listResults.mockResolvedValue([failed])
    api.retryResult.mockResolvedValue(running)

    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.tests.results')!.trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] article')).toHaveLength(1)
    expect(wrapper.get('[data-dialog]').text()).toContain('upstream failed')

    await wrapper.find('[data-dialog] article').findAll('button').find(b => b.text() === 'admin.tests.retry')!.trigger('click')
    await flushPromises()

    expect(api.retryResult).toHaveBeenCalledWith(101)
    const cards = wrapper.findAll('[data-dialog] article')
    expect(cards).toHaveLength(1)
    expect(cards[0].text()).toContain('running')
    expect(cards[0].text()).not.toContain('upstream failed')
    expect(cards[0].text()).not.toContain('old output')
    wrapper.unmount()
  })

  it('switches plans by test type and uses the selected type when creating a plan', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true, sort_order: 20 },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw a pelican', enabled: true, sort_order: 10 },
    ])
    api.listPlans.mockResolvedValue([
      { ...plan, id: 10, test_definition_id: 2, name: 'Candy plan' },
      { ...plan, id: 11, test_definition_id: 3, name: 'Pelican plan' },
    ])
    const wrapper = makeWrapper(); await flushPromises()

    expect(wrapper.find('[data-testid="plan-tab-3"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.text()).toContain('Pelican plan')
    expect(wrapper.text()).not.toContain('Candy plan')

    await wrapper.get('[data-testid="plan-tab-2"]').trigger('click')
    expect(wrapper.text()).toContain('Candy plan')
    expect(wrapper.text()).not.toContain('Pelican plan')

    await wrapper.get('[data-testid="plan-create"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect(dialog.find('select').find('option:checked').text()).toBe('Candy')
    wrapper.unmount()
  })

  it('persists the configured user-facing type order', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    const orderInput = wrapper.find('input[type="number"]')
    await orderInput.setValue('7')
    await orderInput.trigger('change')
    await flushPromises()
    expect(api.updateType).toHaveBeenCalledWith(2, { sort_order: 7 })
    wrapper.unmount()
  })
})
