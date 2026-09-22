import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, afterEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
import AdminTestResultHistory from '@/components/tests/AdminTestResultHistory.vue'
import Select from '@/components/common/Select.vue'

const api = vi.hoisted(() => ({
  listTypes: vi.fn(), listPlans: vi.fn(), createType: vi.fn(), updateType: vi.fn(), deleteType: vi.fn(),
  createPlan: vi.fn(), updatePlan: vi.fn(), deletePlan: vi.fn(), runPlan: vi.fn(), listResults: vi.fn(),
  retryResult: vi.fn(), deleteResult: vi.fn(), getGroups: vi.fn(), getAccounts: vi.fn(), getModelAllowlistCandidates: vi.fn(), success: vi.fn(), error: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { tests: api, groups: { getAll: api.getGroups, getModelAllowlistCandidates: api.getModelAllowlistCandidates }, accounts: { list: api.getAccounts } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))

const plan = { id: 10, name: 'Candy hourly', test_definition_id: 2, test_definition_ids: [2], group_ids: [8], model_id: 'test-model', cron_expression: '0 * * * *', enabled: true, max_results: 50 }
const makeWrapper = () => mount(TestManagementView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: { tests: { elapsed: ({ named }: { named: (key: string) => string }) => `Elapsed ${named('duration')}` } } } } })],
  stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true, BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h3>{{ title }}</h3><slot /><slot name="footer" /></section>' } }
} })

beforeEach(() => {
  vi.resetAllMocks()
  api.listTypes.mockResolvedValue([{ id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count candies', enabled: true }])
  api.listPlans.mockResolvedValue([plan])
  api.getGroups.mockResolvedValue([{ id: 8, name: 'Group Eight', platform: 'openai', status: 'active' }])
  api.getModelAllowlistCandidates.mockResolvedValue(['test-model'])
  api.getAccounts.mockResolvedValue({ items: [{ id: 3, name: 'Account Three', group_ids: [8] }, { id: 4, name: 'Other Group Account', group_ids: [9] }], pages: 1, total: 2 })
  api.listResults.mockResolvedValue([])
  api.runPlan.mockResolvedValue(undefined)
  api.retryResult.mockResolvedValue(undefined)
  api.deleteResult.mockResolvedValue(undefined)
})
afterEach(() => vi.useRealTimers())

describe('configurable test management', () => {
  it('creates, edits and copies model consistency definitions without a user prompt', async () => {
    const modelType = { id: 7, name: 'Model consistency', key: 'model-consistency', output_kind: 'model_check', prompt: 'Server managed prompt', enabled: true }
    api.listTypes.mockResolvedValue([modelType])
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.get('article').text()).toContain('admin.tests.modelCheck')
    await wrapper.get('article button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createType).toHaveBeenCalledWith(expect.objectContaining({ name: 'Model consistency（Copy）', output_kind: 'model_check', prompt: '' }))
    await wrapper.get('article button[aria-label="common.edit"]').trigger('click')
    let dialog = wrapper.get('[data-dialog]')
    expect(dialog.find('textarea').exists()).toBe(false)
    expect(dialog.find('[data-model-check-hint]').exists()).toBe(true)
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.updateType).toHaveBeenCalledWith(7, expect.objectContaining({ output_kind: 'model_check', prompt: '' }))
    await wrapper.findAll('button').find(button => button.text().includes('common.create'))!.trigger('click')
    dialog = wrapper.get('[data-dialog]')
    await dialog.findAll('input')[0].setValue('New check')
    await dialog.findAll('input')[1].setValue('new-check')
    await dialog.get('textarea').setValue('Should not be submitted')
    await dialog.get('[data-type-kind]').setValue('model_check')
    expect(dialog.find('textarea').exists()).toBe(false)
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click'); await flushPromises()
    expect(api.createType).toHaveBeenLastCalledWith(expect.objectContaining({ output_kind: 'model_check', prompt: '', name: 'New check' }))
    wrapper.unmount()
  })

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
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    const dialog = wrapper.get('[data-dialog]')
    expect((dialog.get('[data-testid="plan-type-2"]').element as HTMLInputElement).checked).toBe(true)
    expect((dialog.get('[data-testid="plan-type-3"]').element as HTMLInputElement).checked).toBe(true)
    await dialog.findAll('button').find(button => button.text() === 'common.save')!.trigger('click')
    await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ test_definition_ids: [3, 2], group_ids: [8] }))
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
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ test_definition_ids: [2, 3], group_ids: [8] }))
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
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ name: 'Candy hourly（Copy）', test_definition_ids: [2, 3], group_ids: [8], sort_order: 7, enabled: false }))
    wrapper.unmount()
  })

  it('does not save a rule without any selected check type', async () => {
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click'); await flushPromises()
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
  const protection = { enabled: true, rules: [{ test_definition_id: 2, priority: 0, required_pass: false, pause_on_failure: true, expected_answer: '29', answer_match: 'numeric', thresholds: [], vote: { enabled: false, reject_above: 0, pass_at_least: 3 } }] }






  it('saves and copies nested combined rules and preserves dangling conditions when a check is deselected', async () => {
    const combinations = [{ id: 'route', name: 'Route to A', priority: 2,
      condition: { operator: 'all', conditions: [{ operator: 'any', conditions: [{ operator: 'test', test_definition_id: 2, verdict: 'pass' }] }] },
      action: { scheduling: 'resume', group_mode: 'assign', group_ids: [8] },
    }]
    const combinedProtection = { ...protection, mode: 'combined', combinations }
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3], protection: combinedProtection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    const dialog = wrapper.get('[data-dialog]')
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    await dialog.get('[data-combination-name]').setValue('Updated routing')
    await dialog.get('[data-condition-verdict]').setValue('fail')
    expect(combinations[0].name).toBe('Route to A')
    expect(combinations[0].condition.conditions[0].conditions[0].verdict).toBe('pass')
    await dialog.get('[data-testid="plan-type-2"]').setValue(false)
    expect(dialog.find('[data-missing-condition-test]').exists()).toBe(true)
    expect(save.attributes('disabled')).toBeDefined()
    await dialog.get('[data-testid="plan-type-2"]').setValue(true)
    await dialog.get('[data-protection-type="2"] [data-rule-enabled]').setValue(true)
    expect(dialog.find('[data-missing-condition-test]').exists()).toBe(false)
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ protection: expect.objectContaining({ mode: 'combined', combinations: [expect.objectContaining({ name: 'Updated routing', condition: { operator: 'all', conditions: [{ operator: 'any', conditions: [{ operator: 'test', test_definition_id: 2, verdict: 'fail' }] }] } })] }) }))
    await wrapper.get('tbody button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, protection: combinedProtection }))
    api.createPlan.mock.calls[0][0].protection.combinations[0].condition.conditions[0].conditions[0].verdict = 'fail'
    api.createPlan.mock.calls[0][0].protection.combinations[0].action.group_ids.push(99)
    expect(combinations[0].condition.conditions[0].conditions[0].verdict).toBe('pass')
    expect(combinations[0].action.group_ids).toEqual([8])
    wrapper.unmount()
  })

  it('saves and copies cache recovery settings without mutating the loaded plan', async () => {
    const recovery = { enabled: true, cooldown_seconds: 300, trial_seconds: 300, max_requests: 20, min_samples: 10, recover_rate: 85 }
    const cacheProtection = { enabled: true, rules: [{ test_definition_id: 7, priority: 0, required_pass: false, thresholds: [{ metric: 'cache_rate', operator: 'lt', value: 80 }], on_pass: { scheduling: 'resume', group_mode: 'keep' }, on_fail: { scheduling: 'pause', group_mode: 'keep' }, recovery }] }
    api.listTypes.mockResolvedValue([{ id: 7, name: 'Statistics', key: 'stats', output_kind: 'statistics', prompt: '', enabled: true }])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_id: 7, test_definition_ids: [7], target_mode: 'account', account_id: 3, protection: cacheProtection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    const dialog = wrapper.get('[data-dialog]')
    await dialog.get('[data-recovery-rate]').setValue(90)
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeUndefined()
    await dialog.get('[data-recovery-min-samples]').setValue(21)
    expect(save.attributes('disabled')).toBeDefined()
    await dialog.get('[data-recovery-min-samples]').setValue(10)
    await save.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ protection: expect.objectContaining({ rules: [expect.objectContaining({ recovery: { ...recovery, recover_rate: 90 } })] }) }))
    expect(recovery.recover_rate).toBe(85)
    await wrapper.get('tbody button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ protection: cacheProtection }))
    api.createPlan.mock.calls[0][0].protection.rules[0].recovery.cooldown_seconds = 600
    expect(recovery.cooldown_seconds).toBe(300)
    wrapper.unmount()
  })

  it('saves multiple action targets from the source platform and preserves the loaded plan on edits', async () => {
    const actions = {
      ...protection,
      rules: [{ ...protection.rules[0], on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] }, on_fail: { scheduling: 'keep', group_mode: 'assign', group_ids: [8] } }],
    }
    api.listPlans.mockResolvedValue([{ ...plan, group_ids: [8, 9, 10], protection: actions }])
    api.getGroups.mockResolvedValue([{ id: 8, name: 'Basic', platform: 'openai', status: 'active' }, { id: 9, name: 'Premium', platform: 'openai', status: 'active' }, { id: 10, name: 'Priority', platform: 'openai', status: 'active' }, { id: 11, name: 'Claude', platform: 'anthropic', status: 'active' }, { id: 12, name: 'Inactive', platform: 'openai', status: 'inactive' }, { id: 13, name: 'Composite', platform: 'composite', status: 'active' }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
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





  it('retains disabled selected group IDs for correction instead of silently removing them', async () => {
    const actionProtection = { ...protection, rules: [{ ...protection.rules[0], on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] } }] }
    api.listPlans.mockResolvedValue([{ ...plan, target_mode: 'account', account_id: 3, protection: actionProtection }])
    api.getGroups.mockResolvedValue([{ id: 8, name: 'Basic', platform: 'openai', status: 'active' }, { id: 9, name: 'Disabled', platform: 'openai', status: 'inactive' }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
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
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click'); await flushPromises()
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




  it('blocks saving a protected disabled definition while retaining its configured answer', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: false },
      { id: 3, name: 'Pelican', key: 'pelican', output_kind: 'html', prompt: 'Draw', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3], account_id: 3, target_mode: 'account', protection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    const dialog = wrapper.get('[data-dialog]')
    expect((dialog.get('[data-rule-answer]').element as HTMLTextAreaElement).value).toBe('29')
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    await save.trigger('click')
    expect(api.updatePlan).not.toHaveBeenCalled()
    await dialog.get('[data-protection-enabled]').setValue(false)
    expect(save.attributes('disabled')).toBeDefined()
    await dialog.get('[data-testid="plan-type-2"]').setValue(false)
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ test_definition_ids: [3], protection: { enabled: false, rules: [] } }))
    wrapper.unmount()
  })

  it('opens a disabled copy for correction when a selected definition has been disabled', async () => {
    api.listTypes.mockResolvedValue([
      { id: 2, name: 'Retired check', key: 'retired', output_kind: 'number', prompt: 'Count', enabled: false },
      { id: 3, name: 'Current check', key: 'current', output_kind: 'text', prompt: 'Answer', enabled: true },
    ])
    api.listPlans.mockResolvedValue([{ ...plan, test_definition_ids: [2, 3], protection: { enabled: false, rules: [] } }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody tr button[aria-label="common.copy"]').trigger('click'); await flushPromises()
    expect(api.createPlan).not.toHaveBeenCalled()
    const dialog = wrapper.get('[data-dialog]')
    const save = dialog.findAll('button').find(button => button.text() === 'common.save')!
    expect(save.attributes('disabled')).toBeDefined()
    expect((dialog.get('[data-testid="plan-type-2"]').element as HTMLInputElement).checked).toBe(true)
    await dialog.get('[data-testid="plan-type-2"]').setValue(false)
    expect(save.attributes('disabled')).toBeUndefined()
    await save.trigger('click'); await flushPromises()
    expect(api.createPlan).toHaveBeenCalledWith(expect.objectContaining({ name: 'Candy hourly（Copy）', enabled: false, test_definition_ids: [3] }))
    expect(api.updatePlan).not.toHaveBeenCalled()
    expect(plan.test_definition_ids).toEqual([2])
    wrapper.unmount()
  })

})


describe('general strategies across ordered groups', () => {
  const strategyGroups = [
    { id: 8, name: 'Premium', platform: 'openai', status: 'active' },
    { id: 9, name: 'Standard', platform: 'openai', status: 'active' },
    { id: 10, name: 'Reserve', platform: 'openai', status: 'active' },
    { id: 11, name: 'Different platform', platform: 'anthropic', status: 'active' },
    { id: 12, name: 'Inactive', platform: 'openai', status: 'inactive' },
    { id: 13, name: 'Composite', platform: 'composite', status: 'active' },
  ]
  const saveButton = (wrapper: ReturnType<typeof makeWrapper>) => wrapper.get('[data-dialog]').findAll('button').find(button => button.text() === 'common.save')!

  it('saves ordered group arrays and generic conditions without any legacy target or special mode', async () => {
    api.getGroups.mockResolvedValue(strategyGroups)
    api.listPlans.mockResolvedValue([{ ...plan, group_ids: [8, 9, 10] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-plan-mode]').exists()).toBe(false)
    expect(wrapper.find('[data-protection-editor]').exists()).toBe(true)
    await wrapper.get('[data-selected-plan-group="10"] [data-group-up]').trigger('click')
    expect(wrapper.findAll('[data-selected-plan-group]').map(row => row.attributes('data-selected-plan-group'))).toEqual(['8', '10', '9'])
    await wrapper.get('[data-protection-enabled]').setValue(true)
    await wrapper.get('[data-rule-priority]').setValue(200)
    await wrapper.get('[data-rule-required-pass]').setValue(true)
    await wrapper.get('[data-rule-answer]').setValue('7')
    await wrapper.get('[data-outcome="pass"] [data-action-group-mode]').setValue('assign')
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined()
    expect(wrapper.find('[data-outcome="pass"] [data-action-group="11"]').exists()).toBe(false)
    await wrapper.get('[data-outcome="pass"] [data-action-group="8"]').setValue(true)
    expect(saveButton(wrapper).attributes('disabled')).toBeUndefined()
    await saveButton(wrapper).trigger('click'); await flushPromises()
    const payload = api.updatePlan.mock.calls[0][1]
    expect(payload.group_ids).toEqual([8, 10, 9])
    expect(payload.test_definition_ids).toEqual([2])
    expect(payload).not.toHaveProperty('group_id')
    expect(payload).not.toHaveProperty('account_id')
    expect(payload).not.toHaveProperty('target_mode')
    expect(payload).not.toHaveProperty('test_definition_id')
    expect(payload.protection).not.toHaveProperty('group_workflow')
    expect(payload.protection.rules[0]).toMatchObject({ priority: 200, required_pass: true, expected_answer: '7', on_pass: { group_mode: 'assign', group_ids: [8] } })
    wrapper.unmount()
  })

  it('limits destination groups to the selected strategy and rejects stale destinations when a group is removed', async () => {
    const protection = { enabled: true, rules: [{ test_definition_id: 2, priority: 1, required_pass: false, expected_answer: '7', answer_match: 'numeric', on_pass: { scheduling: 'keep', group_mode: 'assign', group_ids: [9] } }] }
    api.getGroups.mockResolvedValue(strategyGroups)
    api.listPlans.mockResolvedValue([{ ...plan, group_ids: [8, 9], protection }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    expect(wrapper.find('[data-outcome="pass"] [data-action-group="10"]').exists()).toBe(false)
    expect(wrapper.get('[data-plan-group="11"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-plan-group="12"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-plan-group="13"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-plan-group="9"]').setValue(false); await flushPromises()
    expect(wrapper.get('[data-unavailable-groups]').text()).toContain('admin.tests.protection.actions.unavailableGroups')
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined()
    await wrapper.get('[data-outcome="pass"] [data-action-group-mode]').setValue('keep')
    expect(saveButton(wrapper).attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-plan-group="8"]').setValue(false); await flushPromises()
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('intersects supported models and reasoning levels across all groups', async () => {
    api.getGroups.mockResolvedValue(strategyGroups)
    api.listPlans.mockResolvedValue([{ ...plan, group_ids: [8, 9], model_id: 'gpt-6-astra', reasoning_effort: 'ultra' }])
    api.getModelAllowlistCandidates.mockImplementation(async (id: number) => id === 8 ? ['gpt-6-astra', 'only-premium'] : ['standard-only', 'gpt-6-astra'])
    api.getAccounts.mockResolvedValue({ items: [
      { id: 1, group_ids: [8], extra: { upstream_model_metadata: { models: { 'gpt-6-astra': { supported_reasoning_levels: ['medium', 'high', 'ultra'] } } } } },
      { id: 2, group_ids: [9], extra: { upstream_model_metadata: { models: { 'gpt-6-astra': { supported_reasoning_levels: ['low', 'high'] } } } } },
    ], pages: 1, total: 2 })
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    expect(wrapper.getComponent(Select).props('options')).toEqual([{ value: 'gpt-6-astra', label: 'gpt-6-astra' }])
    const effort = wrapper.get('[data-dialog]').findAll('select').find(select => select.find('option[value="high"]').exists())!
    expect(effort.find('option[value="ultra"]').exists()).toBe(false)
    expect(effort.find('option[value="low"]').exists()).toBe(false)
    expect((effort.element as HTMLSelectElement).value).not.toBe('ultra')
    await effort.setValue('high')
    await saveButton(wrapper).trigger('click'); await flushPromises()
    expect(api.updatePlan).toHaveBeenCalledWith(10, expect.objectContaining({ group_ids: [8, 9], reasoning_effort: 'high' }))
    wrapper.unmount()
  })

  it('rejects groups without a common model and ignores an older catalog response', async () => {
    api.getGroups.mockResolvedValue(strategyGroups)
    let resolveOld!: (models: string[]) => void
    api.getModelAllowlistCandidates.mockReturnValueOnce(new Promise(resolve => { resolveOld = resolve }))
      .mockImplementation(async (id: number) => id === 8 ? ['shared'] : ['other'])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    await wrapper.get('[data-plan-group="9"]').setValue(true); await flushPromises()
    expect(wrapper.getComponent(Select).props('options')).toEqual([])
    expect(wrapper.find('[data-no-common-model]').exists()).toBe(true)
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined()
    resolveOld(['test-model']); await flushPromises()
    expect(wrapper.getComponent(Select).props('options')).toEqual([])
    expect(saveButton(wrapper).attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('shows migrated strategy notes and sends only corrected strategy fields', async () => {
    api.listPlans.mockResolvedValue([{ ...plan, enabled: false, migration_note: 'Choose a replacement for a removed group' }])
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.get('[data-migration-note]').text()).toContain('Choose a replacement')
    expect(wrapper.get('tbody button[aria-label="admin.tests.run"]').attributes('disabled')).toBeDefined()
    await wrapper.get('tbody button[role="switch"]').trigger('click'); await flushPromises()
    expect(api.updatePlan).not.toHaveBeenCalled()
    expect(wrapper.get('[data-plan-migration-note]').text()).toContain('Choose a replacement')
    await saveButton(wrapper).trigger('click'); await flushPromises()
    expect(api.updatePlan.mock.calls[0][1]).not.toHaveProperty('migration_note')
    wrapper.unmount()
  })

  it('allows at most 100 groups and 32 selected test types', async () => {
    const groups = Array.from({ length: 101 }, (_, i) => ({ id: i + 1, name: `Group ${i}`, platform: 'openai', status: 'active' }))
    const types = Array.from({ length: 33 }, (_, i) => ({ id: i + 1, name: `Test ${i}`, key: `test-${i}`, output_kind: 'number', prompt: 'Count', enabled: true }))
    api.getGroups.mockResolvedValue(groups)
    api.listTypes.mockResolvedValue(types)
    api.listPlans.mockResolvedValue([{ ...plan, group_ids: groups.slice(0, 100).map(group => group.id), test_definition_ids: types.slice(0, 32).map(type => type.id) }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.get('tbody button[aria-label="common.edit"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-plan-group="101"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-testid="plan-type-33"]').attributes('disabled')).toBeDefined()
    expect(saveButton(wrapper).attributes('disabled')).toBeUndefined()
    wrapper.unmount()
  })
})
