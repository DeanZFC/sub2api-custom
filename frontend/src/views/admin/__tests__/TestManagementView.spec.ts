import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
import AdminTestResultHistory from '@/components/tests/AdminTestResultHistory.vue'

const api = vi.hoisted(() => ({
  listTypes: vi.fn(), listPlans: vi.fn(), createType: vi.fn(), updateType: vi.fn(), deleteType: vi.fn(),
  createPlan: vi.fn(), updatePlan: vi.fn(), deletePlan: vi.fn(), runPlan: vi.fn(), listResults: vi.fn(),
  retryResult: vi.fn(), deleteResult: vi.fn(), getGroups: vi.fn(), getAccounts: vi.fn(), getModelAllowlistCandidates: vi.fn(), success: vi.fn(), error: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { tests: api, groups: { getAll: api.getGroups, getModelAllowlistCandidates: api.getModelAllowlistCandidates }, accounts: { list: api.getAccounts } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))

const plan = { id: 10, name: 'Candy hourly', test_definition_id: 2, group_id: 8, model_id: 'test-model', cron_expression: '0 * * * *', enabled: true, max_results: 50 }
const makeWrapper = () => mount(TestManagementView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: { tests: { elapsed: ({ named }: { named: (key: string) => string }) => `Elapsed ${named('duration')}` } } } } })],
  stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h3>{{ title }}</h3><slot /><slot name="footer" /></section>' } }
} })

beforeEach(() => {
  vi.clearAllMocks()
  api.listTypes.mockResolvedValue([{ id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true }])
  api.listPlans.mockResolvedValue([plan])
  api.getGroups.mockResolvedValue([{ id: 8, name: 'Group Eight' }])
  api.getModelAllowlistCandidates.mockResolvedValue(['test-model'])
  api.getAccounts.mockResolvedValue({ items: [{ id: 3, name: 'Account Three', group_ids: [8] }, { id: 4, name: 'Other Group Account', group_ids: [9] }], pages: 1, total: 2 })
  api.listResults.mockResolvedValue([])
  api.runPlan.mockResolvedValue(undefined)
  api.retryResult.mockResolvedValue(undefined)
  api.deleteResult.mockResolvedValue(undefined)
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
    await dialog.get('[data-type-kind]').setValue('html')
    await dialog.get('textarea').setValue('Draw a pelican riding a motorcycle in HTML.')
    await dialog.findAll('button').find(b => b.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.createType).toHaveBeenCalledWith(expect.objectContaining({ name: 'Pelican', key: 'pelican-custom', output_kind: 'html', prompt: 'Draw a pelican riding a motorcycle in HTML.' }))
    wrapper.unmount()
  })

  it('creates a statistics definition without a prompt and still requires prompts for model tests', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('common.create'))!.trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const inputs = dialog.findAll('input')
    await inputs[0].setValue('Hourly metrics')
    await inputs[1].setValue('hourly-metrics')
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    await dialog.get('[data-type-kind]').setValue('statistics')
    expect(dialog.find('textarea').exists()).toBe(false)
    expect(dialog.find('[data-statistics-hint]').exists()).toBe(true)
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click'); await flushPromises()
    expect(api.createType).toHaveBeenCalledWith(expect.objectContaining({ output_kind: 'statistics', prompt: '', name: 'Hourly metrics' }))
    wrapper.unmount()
  })

  it('edits and copies statistics definitions with empty prompts', async () => {
    const statistics = { id: 7, name: 'Hourly statistics', key: 'hourly_stats', output_kind: 'statistics', prompt: '', enabled: true }
    api.listTypes.mockResolvedValue([statistics])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('article button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createType).toHaveBeenCalledWith(expect.objectContaining({ name: 'Hourly statistics（Copy）', key: 'hourly_stats-copy', output_kind: 'statistics', prompt: '' }))
    await wrapper.get('article button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect(dialog.find('textarea').exists()).toBe(false)
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.updateType).toHaveBeenCalledWith(7, expect.objectContaining({ output_kind: 'statistics', prompt: '' }))
    wrapper.unmount()
  })

  it('selects an account within a group while preserving the group target', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    const row = wrapper.findAll('tbody tr')[0]
    await row.get('button[aria-label="common.edit"]').trigger('click')
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
    await wrapper.get('button[aria-label="admin.tests.run"]').trigger('click')
    await flushPromises()
    expect(api.runPlan).toHaveBeenCalledWith(10)
    expect(wrapper.get('[data-dialog]').text()).toContain('admin.tests.reasoningEffort: ultra')
    api.listResults.mockResolvedValue([{ id: 101, plan_id: 10, test_definition_id: 2, model_id: 'test-model', reasoning_effort: 'ultra', status: 'success', output_kind: 'number', output_numeric: 29, response_text: '最终答案：29', started_at: '2099-01-01T00:00:00Z' }])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-dialog]').text()).toContain('29')
    expect(wrapper.get('[data-dialog]').text()).toContain('admin.tests.reasoningEffort: ultra')
    expect(api.listResults).toHaveBeenLastCalledWith(10, 1)
    wrapper.unmount()
    const count = api.listResults.mock.calls.length
    await vi.advanceTimersByTimeAsync(5000)
    expect(api.listResults).toHaveBeenCalledTimes(count)
  })

  it('loads history on demand and only polls the latest results', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('button[aria-label="admin.tests.results"]').trigger('click')
    await flushPromises()
    expect(api.listResults).toHaveBeenLastCalledWith(10, 1)
    const dialog = wrapper.get('[data-dialog]')
    await dialog.findAll('button').find(button => button.text() === 'admin.tests.showHistory')!.trigger('click')
    await flushPromises()
    expect(api.listResults).toHaveBeenLastCalledWith(10, 50)
    expect(dialog.get('button[role="tab"][aria-selected="true"]').text()).toBe('admin.tests.showHistory')
    const historyCalls = api.listResults.mock.calls.length
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.listResults).toHaveBeenCalledTimes(historyCalls)
    await dialog.findAll('button').find(button => button.text() === 'common.refresh')!.trigger('click')
    await flushPromises()
    expect(api.listResults).toHaveBeenCalledTimes(historyCalls + 1)
    expect(api.listResults).toHaveBeenLastCalledWith(10, 50)
    await dialog.findAll('button').find(button => button.text() === 'admin.tests.latestResults')!.trigger('click')
    await flushPromises()
    expect(api.listResults).toHaveBeenLastCalledWith(10, 1)
    const latestCalls = api.listResults.mock.calls.length
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(api.listResults).toHaveBeenCalledTimes(latestCalls + 1)
    wrapper.unmount()
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
    await wrapper.get('button[aria-label="admin.tests.results"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] article')).toHaveLength(1)
    expect(wrapper.get('[data-dialog]').text()).toContain('upstream failed')

    await wrapper.find('[data-dialog] article').get('button[aria-label="admin.tests.retry"]').trigger('click')
    await flushPromises()

    expect(api.retryResult).toHaveBeenCalledWith(101)
    const cards = wrapper.findAll('[data-dialog] article')
    expect(cards).toHaveLength(1)
    expect(cards[0].text()).toContain('running')
    expect(cards[0].text()).not.toContain('upstream failed')
    expect(cards[0].text()).not.toContain('old output')
    wrapper.unmount()
  })

  it('shows elapsed time for running results and final latency only after completion', async () => {
    vi.useFakeTimers({ toFake: ['Date', 'setInterval', 'clearInterval'] })
    vi.setSystemTime(new Date('2026-09-21T06:09:16Z'))
    const running = { id: 101, plan_id: 10, test_definition_id: 2, status: 'running', output_kind: 'number', latency_ms: 0, started_at: '2026-09-21T06:07:11Z' }
    api.listResults.mockResolvedValue([running])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('button[aria-label="admin.tests.results"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[data-dialog] article').text()).toContain('Elapsed 02:05')
    expect(wrapper.get('[data-dialog] article').text()).not.toContain('0ms')

    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-dialog] article').text()).toContain('Elapsed 02:10')

    api.listResults.mockResolvedValue([{ ...running, status: 'success', latency_ms: 130123, output_numeric: 29 }])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    expect(wrapper.get('[data-dialog] article').text()).toContain('130123ms')
    expect(wrapper.get('[data-dialog] article').text()).not.toContain('Elapsed')
    wrapper.unmount()
  })

  it('keeps every selected type when reopening a plan with a legacy scalar definition', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw a pelican', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_id: 3, test_definition_ids: [3, 2] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect((dialog.get('[data-testid="plan-type-2"]').element as HTMLInputElement).checked).toBe(true)
    expect((dialog.get('[data-testid="plan-type-3"]').element as HTMLInputElement).checked).toBe(true)
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ test_definition_id: 3, test_definition_ids: [3, 2] }))
    wrapper.unmount()
  })

  it('lists every rule together and saves several check types in one rule', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true, sort_order: 20 },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw a pelican', enabled: true, sort_order: 10 },
    ])
    api.listPlans.mockResolvedValue([
      { ...plan, id: 10, test_definition_id: 2, name: 'Candy plan' },
      { ...plan, id: 11, test_definition_id: 3, name: 'Pelican plan' },
    ])
    const wrapper = makeWrapper(); await flushPromises()

    expect(wrapper.find('[role="tablist"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('Pelican plan')
    expect(wrapper.text()).toContain('Candy plan')
    await wrapper.findAll('tbody tr')[0].get('button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect((dialog.get('[data-testid="plan-type-2"]').element as HTMLInputElement).checked).toBe(true)
    await dialog.get('[data-testid="plan-type-3"]').setValue(true)
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ test_definition_id: 2, test_definition_ids: [2, 3] }))
    wrapper.unmount()
  })

  it('keeps all selected types and display order when copying a rule', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw a pelican', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3], sort_order: 7, target_mode: 'account', account_id: 3 }])
    const wrapper = makeWrapper(); await flushPromises()
    const row = wrapper.get('tbody tr')
    expect(row.text()).toContain('Candy')
    expect(row.text()).toContain('Pelican')
    await row.get('button[aria-label="common.copy"]').trigger('click')
    await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ name: 'Candy hourly（Copy）', test_definition_id: 2, test_definition_ids: [2, 3], sort_order: 7, target_mode: 'account', account_id: 3 }))
    wrapper.unmount()
  })

  it('does not save a rule without any selected check type', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click')
    await wrapper.get('[data-testid="plan-type-2"]').setValue(false)
    const saveButton = wrapper.get('[data-dialog]').findAll('button').find(button => button.text() === 'common.save')!
    expect(saveButton.attributes('disabled')).toBeDefined()
    await saveButton.trigger('click')
    expect(api.updatePlan).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('shows a running row for each configured type until its result arrives', async () => {
    vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw a pelican', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('button[aria-label="admin.tests.run"]').trigger('click')
    await flushPromises()
    expect(wrapper.findAll('[data-dialog] article')).toHaveLength(2)
    api.listResults.mockResolvedValue([{ id: 101, plan_id: 10, test_definition_id: 2, test_name: 'Candy', model_id: 'test-model', status: 'success', output_kind: 'number', output_numeric: 29, started_at: '2099-01-01T00:00:00Z' }])
    await vi.advanceTimersByTimeAsync(5000); await flushPromises()
    const rows = wrapper.findAll('[data-dialog] article')
    expect(rows).toHaveLength(2)
    expect(rows.find(row => row.text().includes('Pelican'))!.text()).toContain('running')
    expect(rows.find(row => row.text().includes('Candy'))!.text()).not.toContain('running')
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

  it.each(['retry', 'delete'] as const)('ignores an older refresh after %s completes', async action => {
    const failed = { id: 101, plan_id: 10, account_id: 3, account_name: 'Account Three', status: 'failed', output_kind: 'number', error_message: 'old error' }
    api.listResults.mockResolvedValue([failed])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('button[aria-label="admin.tests.results"]').trigger('click')
    await flushPromises()
    const pane = wrapper.getComponent(AdminTestResultHistory)
    let resolveOld!: (rows: typeof failed[]) => void
    api.listResults.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    await wrapper.get('[data-dialog]').findAll('button').find(button => button.text() === 'common.refresh')!.trigger('click')
    const confirm = vi.spyOn(window, 'confirm').mockReturnValue(true)
    if (action === 'retry') {
      api.retryResult.mockResolvedValue({ ...failed, account_name: undefined, status: 'running', error_message: '' })
      pane.vm.$emit('retry', failed)
    } else {
      api.listResults.mockResolvedValue([])
      pane.vm.$emit('delete', failed)
    }
    await flushPromises()
    resolveOld([failed]); await flushPromises()
    const rows = pane.props('results')
    if (action === 'retry') {
      expect(rows).toHaveLength(1)
      expect(rows[0]).toMatchObject({ status: 'running', account_name: 'Account Three' })
    } else {
      expect(rows).toHaveLength(0)
      expect(api.deleteResult).toHaveBeenCalledWith(101)
    }
    confirm.mockRestore()
    wrapper.unmount()
  })

  it('does not let an old rule request overwrite a newly opened rule', async () => {
    api.listPlans.mockResolvedValue([plan, { ...plan, id: 11, name: 'Other rule' }])
    let resolveOld!: (rows: unknown[]) => void
    api.listResults.mockImplementationOnce(() => new Promise(resolve => { resolveOld = resolve }))
    api.listResults.mockResolvedValueOnce([{ id: 202, plan_id: 11, account_id: 4, account_name: 'Other account', status: 'success', output_kind: 'number', output_numeric: 22 }])
    const wrapper = makeWrapper(); await flushPromises()
    const buttons = wrapper.findAll('button[aria-label="admin.tests.results"]')
    await buttons[0].trigger('click')
    await buttons[1].trigger('click'); await flushPromises()
    resolveOld([{ id: 101, plan_id: 10, status: 'failed', output_kind: 'text' }]); await flushPromises()
    expect(wrapper.getComponent(AdminTestResultHistory).props('results').map(row => row.id)).toEqual([202])
    expect(wrapper.getComponent(AdminTestResultHistory).props('loading')).toBe(false)
    wrapper.unmount()
  })
})


describe('automatic protection plan integration', () => {
  const protection = { enabled: true, rules: [{ test_definition_id: 2, pause_on_failure: true, expected_answer: '29', answer_match: 'numeric', thresholds: [], vote: { enabled: false, reject_above: 0, pass_at_least: 3 } }] }
  it('saves multiple action targets from the source platform and preserves the loaded plan on edits', async () => {
    const actions = {
      ...protection,
      rules: [{ ...protection.rules[0], on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'keep', group_mode: 'assign', group_ids: [8] } }],
    }
    api.listPlans.mockResolvedValue([{ ...plan, target_mode: 'account', account_id: 3, protection: actions }])
    api.getGroups.mockResolvedValue([{ id: 8, name: 'Basic', platform: 'openai', status: 'active' }, { id: 9, name: 'Premium', platform: 'openai', status: 'active' }, { id: 10, name: 'Priority', platform: 'openai', status: 'active' }, { id: 11, name: 'Claude', platform: 'anthropic', status: 'active' }, { id: 12, name: 'Inactive', platform: 'openai', status: 'inactive' }, { id: 13, name: 'Composite', platform: 'composite', status: 'active' }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const pass = dialog.get('[data-outcome="pass"]')
    expect(pass.find('[data-action-group="11"]').exists()).toBe(false)
    expect(pass.find('[data-action-group="12"]').exists()).toBe(false)
    expect(pass.find('[data-action-group="13"]').exists()).toBe(false)
    await pass.get('[data-action-group="10"]').setValue(true)
    expect(actions.rules[0].on_pass.group_ids).toEqual([9])
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ protection: expect.objectContaining({ rules: [expect.objectContaining({ on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9, 10] } })] }) }))
    await wrapper.get('tbody button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ protection: actions }))
    const copied = api.createPlan.mock.calls[0][0]
    copied.protection.rules[0].on_pass.group_ids.push(10)
    expect(actions.rules[0].on_pass.group_ids).toEqual([9])
    wrapper.unmount()
  })

  it('keeps an existing account target visible after automation moved it out of the source group', async () => {
    api.listPlans.mockResolvedValue([{ ...plan, target_mode: 'account', account_id: 3, protection }])
    api.getAccounts.mockResolvedValue({ items: [{ id: 3, name: 'Moved Account', group_ids: [9] }, { id: 4, name: 'Unrelated Account', group_ids: [9] }], pages: 1, total: 2 })
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const accountSelect = dialog.findAll('select').find(select => select.find('option[value="3"]').exists())!
    expect(accountSelect.text()).toContain('Moved Account')
    expect(accountSelect.find('option[value="4"]').exists()).toBe(false)
    expect((accountSelect.element as HTMLSelectElement).value).toBe('3')
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ group_id: 8, account_id: 3 }))
    wrapper.unmount()
  })

  it('opens a moved-account copy for correction and prevents creating it until a current member is selected', async () => {
    api.listPlans.mockResolvedValue([{ ...plan, target_mode: 'account', account_id: 3, protection }])
    api.getAccounts.mockResolvedValue({ items: [{ id: 3, name: 'Moved Account', group_ids: [9] }, { id: 4, name: 'Current Member', group_ids: [8] }], pages: 1, total: 2 })
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createPlan).not.toHaveBeenCalled()
    const dialog = wrapper.get('[data-dialog]')
    expect(dialog.find('[data-invalid-account-target]').exists()).toBe(true)
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    const accountSelect = dialog.findAll('select').find(select => select.find('option[value="4"]').exists())!
    expect(accountSelect.find('option[value="3"]').exists()).toBe(false)
    await accountSelect.setValue('4')
    expect(dialog.find('[data-invalid-account-target]').exists()).toBe(false)
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click'); await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ name: 'Candy hourly（Copy）', group_id: 8, account_id: 4, protection }))
    expect(api.updatePlan).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('retains disabled selected group IDs for correction instead of silently removing them', async () => {
    const actionProtection = { ...protection, rules: [{ ...protection.rules[0], on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] } }] }
    api.listPlans.mockResolvedValue([{ ...plan, target_mode: 'account', account_id: 3, protection: actionProtection }])
    api.getGroups.mockResolvedValue([{ id: 8, name: 'Basic', platform: 'openai', status: 'active' }, { id: 9, name: 'Disabled', platform: 'openai', status: 'inactive' }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const pass = dialog.get('[data-outcome="pass"]')
    expect(pass.find('[data-action-group="9"]').exists()).toBe(false)
    expect(pass.get('[data-selected-groups]').text()).toContain('#9')
    expect(pass.find('[data-unavailable-groups]').exists()).toBe(true)
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    expect(actionProtection.rules[0].on_pass.group_ids).toEqual([9])
    await pass.get('[data-action-group-mode]').setValue('keep')
    expect(save.attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
  it('edits and copies protection without mutating the loaded plan', async () => {
    api.listPlans.mockResolvedValue([{ ...plan, target_mode: 'account', account_id: 3, protection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect((dialog.get('[data-protection-enabled]').element as HTMLInputElement).checked).toBe(true)
    await dialog.get('[data-rule-answer]').setValue('30')
    expect(protection.rules[0].expected_answer).toBe('29')
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ protection: expect.objectContaining({ enabled: true, rules: [expect.objectContaining({ expected_answer: '30' })] }) }))
    await wrapper.get('tbody tr button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ protection }))
    wrapper.unmount()
  })

  it('removes deselected type protection and turns it off for group checks', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3], target_mode: 'account', account_id: 3, protection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    await dialog.get('[data-testid="plan-type-2"]').setValue(false)
    expect(dialog.find('[data-protection-type="2"]').exists()).toBe(false)
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    const accountSelect = dialog.findAll('select').find(select => select.find('option[value="3"]').exists())!
    await accountSelect.findAll('option')[0].setValue()
    expect((dialog.get('[data-protection-enabled]').element as HTMLInputElement).checked).toBe(false)
    expect(dialog.get('[data-protection-enabled]').attributes('disabled')).toBeDefined()
    await save.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ target_mode: 'group', protection: { enabled: false, rules: [] } }))
    wrapper.unmount()
  })
  it('loads active and quality-paused accounts across pages, deduplicates IDs and keeps manual stops', async () => {
    const paused = { id: 5, name: 'Paused account', group_ids: [8], status: 'quality_paused', schedulable: true }
    const stopped = { id: 6, name: 'Manually stopped', group_ids: [8], status: 'active', schedulable: false }
    api.getAccounts.mockImplementation(async (page: number, _size: number, filter: { status: string }) => {
      if (filter.status === 'quality_paused') return { items: page === 1 ? [paused] : [{ ...paused }, { id: 7, name: 'Paused older account', group_ids: [8], status: 'quality_paused', schedulable: true }], pages: 2, total: 2 }
      return { items: [stopped], pages: 1, total: 1 }
    })
    api.listPlans.mockResolvedValue([{ ...plan, account_id: 5, target_mode: 'account', protection }, { ...plan, id: 11, account_id: 6, target_mode: 'account' }])
    const wrapper = makeWrapper(); await flushPromises()
    expect(api.getAccounts).toHaveBeenCalledWith(1, 1000, { lite: '1', status: 'active' })
    expect(api.getAccounts).toHaveBeenCalledWith(2, 1000, { lite: '1', status: 'quality_paused' })
    expect(wrapper.get('tbody tr').text()).toContain('Paused account (#5)')
    expect(wrapper.findAll('tbody tr')[1].get('button[aria-label="admin.tests.run"]').attributes('disabled')).toBeDefined()
    await wrapper.findAll('tbody tr')[1].get('button[aria-label="admin.tests.run"]').trigger('click')
    expect(api.runPlan).not.toHaveBeenCalled()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const accountSelect = dialog.findAll('select').find(select => select.find('option[value="5"]').exists())!
    expect(accountSelect.findAll('option[value="5"]')).toHaveLength(1)
    expect(accountSelect.find('option[value="6"]').text()).toContain('Manually stopped')
    expect(accountSelect.find('option[value="7"]').exists()).toBe(true)
    expect((accountSelect.element as HTMLSelectElement).value).toBe('5')
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ account_id: 5, protection }))
    wrapper.unmount()
  })

  it('blocks saving a protected disabled definition while retaining its configured answer', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: false },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3], account_id: 3, target_mode: 'account', protection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    expect((dialog.get('[data-rule-answer]').element as HTMLTextAreaElement).value).toBe('29')
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    await save.trigger('click')
    expect(api.updatePlan).not.toHaveBeenCalled()
    await dialog.get('[data-protection-enabled]').setValue(false)
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ protection: { ...protection, enabled: false } }))
    wrapper.unmount()
  })

})
