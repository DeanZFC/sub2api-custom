import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'

const api = vi.hoisted(() => ({
  listTypes: vi.fn(), listPlans: vi.fn(), createType: vi.fn(), updateType: vi.fn(), deleteType: vi.fn(),
  createPlan: vi.fn(), updatePlan: vi.fn(), deletePlan: vi.fn(), runPlan: vi.fn(), listResults: vi.fn(),
  getGroups: vi.fn(), getAccounts: vi.fn(), success: vi.fn(), error: vi.fn()
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
  api.getAccounts.mockResolvedValue({ items: [{ id: 3, name: 'Account Three' }], pages: 1, total: 1 })
  api.listResults.mockResolvedValue([])
  api.runPlan.mockResolvedValue(undefined)
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

  it('switches a group plan to one account with an explicit null group', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    const row = wrapper.findAll('tbody tr')[0]
    await row.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const targetSelect = dialog.findAll('select').find(s => s.find('option[value="account"]').exists())!
    await targetSelect.setValue('account')
    const accountSelect = dialog.findAll('select').find(s => s.find('option[value="3"]').exists())!
    await accountSelect.setValue('3')
    await dialog.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ group_id: null, account_id: 3, model_id: 'test-model', cron_expression: '0 * * * *' }))
    wrapper.unmount()
  })

  it('refreshes retained results after asynchronous execution without a page reload', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'admin.tests.run')!.trigger('click')
    await flushPromises()
    expect(api.runPlan).toHaveBeenCalledWith(10)
    api.listResults.mockResolvedValue([{ id: 101, plan_id: 10, model_id: 'test-model', status: 'success', output_kind: 'number', output_numeric: 29, response_text: '最终答案：29' }])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-dialog]').text()).toContain('29')
    expect(api.listResults).toHaveBeenLastCalledWith(10, 50)
    wrapper.unmount()
    const count = api.listResults.mock.calls.length
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.listResults).toHaveBeenCalledTimes(count)
  })
})
