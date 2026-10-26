import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import PassiveQualityPanel from '../PassiveQualityPanel.vue'
import { defaultPassivePolicy, emptyPassiveSummary } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import type { AdminGroup, AccountListItem } from '@/types'
const api = vi.hoisted(() => ({ policies: vi.fn(), states: vi.fn(), events: vi.fn(), statePage: vi.fn(), stateSummary: vi.fn(), eventPage: vi.fn(), save: vi.fn(), remove: vi.fn(), act: vi.fn(), models: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/admin/passiveQuality', async original => ({ ...await original<object>(), passiveQualityAPI: api }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getModelAllowlistCandidates: api.models } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
const group = { id: 8, name: 'Group', platform: 'openai', status: 'active' } as AdminGroup
const account = { id: 62, name: 'Account A', group_ids: [8] } as AccountListItem
const recordState = (overrides: Record<string, unknown> = {}) => ({
  policy_id: 1, policy_name: 'Policy', account_id: 62, account_name: 'Account A', model: 'model-a', scope: 'model', mode: 'enforce',
  phase: 'healthy', reason: '', hits: 0, backoff: 0, used: 0, inflight: 0, generation: 1, next_at: '', trial_started_at: '', trial_ends_at: '', updated_at: '', other_hold: false,
  evidence: { samples: 20, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, fresh: 0, complete: true, latest: '' },
  ...overrides,
})
const event = (id: number, overrides: Record<string, unknown> = {}) => ({ id, policy_id: 1, account_id: 62, policy_name: 'Policy', kind: 'pause', detail: recordState(), created_at: '2026-10-24T00:00:00Z', ...overrides })
const makeWrapper = (tab = 'passive') => mount(PassiveQualityPanel, { props: { tab, groups: [group], accounts: [account] }, global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
  stubs: { BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' } },
} })
beforeEach(() => { vi.resetAllMocks(); api.policies.mockResolvedValue([]); api.states.mockResolvedValue([]); api.events.mockResolvedValue([]); api.statePage.mockImplementation(async () => ({ items: await api.states(), total: (await api.states()).length, has_more: false })); api.eventPage.mockImplementation(async () => ({ items: await api.events(), has_more: false })); api.stateSummary.mockResolvedValue(emptyPassiveSummary()); api.models.mockResolvedValue(['model']); api.save.mockResolvedValue({}) })
afterEach(() => vi.useRealTimers())
describe('passive quality management', () => {
  it('keeps group targets out of the generic V2 editor and links to their dedicated panel', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 7, name: 'Protected group target', purpose: 'group_quality_target', group_ids: [8] }])
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.findAll('article')).toHaveLength(0)
    expect(wrapper.get('[data-target-managed]').text()).toContain('dedicated editor')
    await wrapper.get('[data-target-managed] button').trigger('click')
    expect(wrapper.emitted('tab')).toEqual([['group-targets']])
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    wrapper.unmount()
  })
  it('labels group target records as actual cache and starts a real-traffic trial', async () => {
    const state = { purpose: 'group_quality_target', policy_id: 7, policy_name: 'Group targets', account_id: 62, model: 'A', scope: 'model', mode: 'enforce', phase: 'paused', reason: 'group_target_trial', used: 0, inflight: 0, other_hold: false, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: 1000, ttft_samples: 10, cache_rate: 0, cache_samples: 10, complete: true } }
    api.states.mockResolvedValue([state])
    const wrapper = makeWrapper('records'); await flushPromises()
    expect(wrapper.text()).toContain('Group quality protection')
    expect(wrapper.text()).toContain('Actual cache usage: 0%')
    expect(wrapper.text()).toContain('Availability: 80%')
    await wrapper.findAll('button').find(button => button.text() === 'Start recovery trial')!.trigger('click'); await flushPromises()
    expect(api.act).toHaveBeenCalledWith(state, 'verify', 0)
    wrapper.unmount()
  })
  it('saves all models independently by default without requiring a model', async () => {
    const wrapper = makeWrapper(); await flushPromises(); await wrapper.get('[data-passive-create]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    await dialog.findAll('input:not([type]), input[type="text"]')[0].setValue('Account protection')
    await dialog.findAll('label').find(l => l.text() === 'Group')!.get('input').setValue(true)
    const model = dialog.get('input[list="passive-models"]')
    expect(model.attributes('required')).toBeUndefined()
    expect(model.attributes('placeholder')).toBe('All models evaluated independently')
    const scope = dialog.findAll('select').find(s => s.find('option[value="account"]').exists())!
    expect((scope.element as HTMLSelectElement).value).toBe('model')
    expect(scope.attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ name: 'Account protection', model: '', scope: 'model', mode: 'observe' }))
    wrapper.unmount()
  })
  it('preserves V2 model scope when the model filter is cleared', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 2, name: 'Policy', model: 'model', scope: 'model', group_ids: [8] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const scope = dialog.findAll('select').find(s => s.find('option[value="account"]').exists())!
    expect((scope.element as HTMLSelectElement).value).toBe('model')
    expect(scope.attributes('disabled')).toBeUndefined()
    await dialog.get('input[list="passive-models"]').setValue('  ')
    expect((scope.element as HTMLSelectElement).value).toBe('model')
    expect(scope.attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ id: 1, version: 2, model: '', scope: 'model' }))
    wrapper.unmount()
  })
  it('starts in observe mode and saves a bounded recovery policy', async () => {
    const wrapper = makeWrapper(); await flushPromises(); await wrapper.get('[data-passive-create]').trigger('click')
    const dialog = wrapper.get('[data-dialog]')
    const textInputs = dialog.findAll('input:not([type]), input[type="text"]')
    await textInputs[0].setValue('Protect provider'); await dialog.get('input[list="passive-models"]').setValue('model')
    const groupLabel = dialog.findAll('label').find(l => l.text() === 'Group')!
    await groupLabel.get('input').setValue(true); await flushPromises()
    expect(wrapper.get('[data-passive-save]').attributes('disabled')).toBeUndefined()
    expect(dialog.text()).toContain('Availability / latency observation window')
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ name: 'Protect provider', mode: 'observe', group_ids: [8], model: 'model', trial_concurrency: 1, error_rate: { enabled: true, pause: 20, recover: 5 } }))
    wrapper.unmount()
  })
  it('rejects impossible recovery sample budgets before saving', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 2, name: 'Policy', model: 'model', group_ids: [8] }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const label = wrapper.get('[data-dialog]').findAll('label').find(l => l.text() === 'Minimum recovery samples')!
    await label.get('input').setValue(200)
    expect(wrapper.get('[data-passive-save]').attributes('disabled')).toBeDefined(); expect(wrapper.get('[data-quality-advanced]').attributes('open')).toBeDefined(); expect(api.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })
  it('shows missing metrics and keeps verify distinct from temporary exemption', async () => {
    const state = { policy_id: 1, policy_name: 'Policy', account_id: 62, model: '', probe_model: 'observed-model', scope: 'account', mode: 'enforce', phase: 'paused', reason: 'insufficient_metric_samples', used: 0, inflight: 0, other_hold: true, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, complete: true } }
    api.states.mockResolvedValue([state]);const wrapper = makeWrapper('records');await flushPromises()
    expect(wrapper.text()).toContain('Another manual, active quality or rate limit hold exists');expect(wrapper.text()).toContain('—')
    expect(wrapper.text()).toContain('All models')
    expect(wrapper.text()).toContain('Recovery probe model: observed-model')
    await wrapper.findAll('button').find(b => b.text() === 'Verify now')!.trigger('click');await flushPromises()
    expect(api.act).toHaveBeenCalledWith(state, 'verify', 0)
    await wrapper.findAll('button').find(b => b.text() === 'Temporary exemption')!.trigger('click')
    await wrapper.get('[data-dialog] input[type="number"]').setValue(15)
    await wrapper.get('[data-dialog]').findAll('button').find(b => b.text() === 'common.confirm')!.trigger('click');await flushPromises()
    expect(api.act).toHaveBeenLastCalledWith(state, 'exempt', 900);wrapper.unmount()
  })
  it('keeps request errors visible and allows retry', async () => {
    api.policies.mockRejectedValueOnce(new Error('offline'))
    const wrapper = makeWrapper();await flushPromises();expect(wrapper.get('[role="alert"]').exists()).toBe(true)
    await wrapper.get('[role="alert"] button').trigger('click');await flushPromises();expect(wrapper.find('[role="alert"]').exists()).toBe(false);wrapper.unmount()
  })
  it('preserves custom values and avoids resetting protection on an unchanged save', async () => {
    const policy = { ...defaultPassivePolicy(), id: 9, version: 3, name: 'Custom', group_ids: [8], window_seconds: 1234, trial_concurrency: 3, mode: 'enforce' as const, model: 'model', scope: 'model' as const }
    api.policies.mockResolvedValue([policy])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    expect(wrapper.get('[data-current-profile]').text()).toContain('Custom')
    expect(wrapper.get('[data-quality-advanced]').attributes('open')).toBeUndefined()
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).not.toHaveBeenCalled()
    expect(wrapper.find('[data-dialog]').exists()).toBe(false)
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('[data-dialog] input:not([type])').setValue('Renamed custom')
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith({ ...policy, name: 'Renamed custom' })
    wrapper.unmount()
  })

  it('applies a profile without enabling enforcement or changing thresholds, then explicitly enables limiting', async () => {
    const policy = { ...defaultPassivePolicy(), id: 9, version: 3, name: 'Custom', group_ids: [8], error_rate: { enabled: true, pause: 17, recover: 3 } }
    api.policies.mockResolvedValue([policy])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('[data-quality-profile="steady"]').trigger('click')
    expect((wrapper.get('[data-v2-mode]').element as HTMLSelectElement).value).toBe('observe')
    expect(wrapper.get('[data-policy-preview]').text()).toContain('No scheduling changes or recovery probes')
    await wrapper.get('[data-v2-mode]').setValue('enforce')
    await wrapper.get('[data-v2-availability-action]').setValue('limit')
    expect(wrapper.get('[data-policy-preview]').text()).toContain('Concurrency cap 2')
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ mode: 'enforce', action: 'limit', min_samples: 60, max_samples: 200, error_rate: policy.error_rate, group_ids: [8], version: 3 }))
    expect(policy.mode).toBe('observe')
    wrapper.unmount()
  })

  it('uses seconds for first-token inputs and prevents empty thresholds from becoming zero', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 2, name: 'Policy', group_ids: [8], ttft: { enabled: true, pause: 10001, recover: 5001 } }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const row = wrapper.findAll('label').find(l => l.text() === 'First token P90 (seconds)')!.element.parentElement!
    const inputs = row.querySelectorAll<HTMLInputElement>('input[type="number"]')
    expect(inputs[0].value).toBe('10.001')
    inputs[0].value = ''; inputs[0].dispatchEvent(new Event('input', { bubbles: true })); await flushPromises()
    expect(wrapper.get('[data-passive-save]').attributes('disabled')).toBeDefined()
    inputs[0].value = '12.345'; inputs[0].dispatchEvent(new Event('input', { bubbles: true })); await flushPromises()
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ ttft: { enabled: true, pause: 12345, recover: 5001 } }))
    wrapper.unmount()
  })

  it('keeps a legacy policy unchanged until an explicit upgrade and resets upgraded drafts to observation', async () => {
    const policy = { ...defaultPassivePolicy(), schema_version: undefined, cache_protection: undefined, ttft_action: undefined, ttft_concurrency_limit: undefined, ttft_priority_penalty: undefined, id: 10, version: 3, name: 'Legacy', group_ids: [8], mode: 'enforce' as const, cache_rate: { enabled: true, pause: 10, recover: 20 } }
    api.policies.mockResolvedValue([policy])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    expect(wrapper.get('[data-legacy-policy]').text()).toContain('Editing does not upgrade it automatically')
    expect(wrapper.find('[data-passive-v2-settings]').exists()).toBe(false)
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('[data-upgrade-policy]').trigger('click')
    expect(wrapper.find('[data-legacy-policy]').exists()).toBe(false)
    expect((wrapper.get('[data-v2-mode]').element as HTMLSelectElement).value).toBe('observe')
    expect(wrapper.get('[data-cache-action]').findAll('option').map(o => o.attributes('value'))).toEqual(['observe', 'reduce'])
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ id: 10, version: 3, schema_version: 2, mode: 'observe', scope: 'model', cache_rate: policy.cache_rate, cache_protection: expect.objectContaining({ action: 'reduce', allocation_percent: 25 }) }))
    wrapper.unmount()
  })

  it('blocks invalid cache controls while keeping cache windows independent of sensitivity profiles', async () => {
    const policy = { ...defaultPassivePolicy(), id: 7, version: 1, name: 'Cache', group_ids: [8], cache_rate: { enabled: true, pause: 10, recover: 20 } }
    api.policies.mockResolvedValue([policy])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('[data-quality-profile="responsive"]').trigger('click')
    expect((wrapper.get('[data-cache-field="action_window.window_seconds"]').element as HTMLInputElement).value).toBe('900')
    await wrapper.get('[data-cache-field="allocation_percent"]').setValue(0)
    expect(wrapper.get('[data-passive-save]').attributes('disabled')).toBeDefined()
    await wrapper.get('[data-cache-field="allocation_percent"]').setValue(25)
    await wrapper.get('[data-cache-field="action_window.window_seconds"]').setValue(120)
    expect(wrapper.text()).toContain('at least as conservative as quick observation')
    await wrapper.get('[data-passive-save]').trigger('click')
    expect(api.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('retains legacy action editing without introducing v2 fields on a renamed policy', async () => {
    const policy = { ...defaultPassivePolicy(), schema_version: 1, cache_protection: undefined, ttft_action: undefined, ttft_concurrency_limit: undefined, ttft_priority_penalty: undefined, id: 10, version: 3, name: 'Legacy', group_ids: [8], mode: 'enforce' as const }
    api.policies.mockResolvedValue([policy])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('[data-dialog] input:not([type])').setValue('Legacy renamed')
    await wrapper.get('[data-quality-response="limit"]').trigger('click')
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ schema_version: 1, name: 'Legacy renamed', mode: 'enforce', action: 'limit' }))
    expect(api.save.mock.calls[0][0]).not.toHaveProperty('cache_protection')
    wrapper.unmount()
  })

  it('supports explicit account scope in V2 and preserves it on ordinary edits', async () => {
    const policy = { ...defaultPassivePolicy(), id: 7, version: 1, name: 'Account-wide', group_ids: [8], scope: 'account' as const }
    api.policies.mockResolvedValue([policy])
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.text()).toContain('All models aggregated (entire account)')
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    const scope = wrapper.get('[data-dialog]').findAll('select').find(s => s.find('option[value="account"]').exists())!
    expect((scope.element as HTMLSelectElement).value).toBe('account')
    expect(scope.attributes('disabled')).toBeUndefined()
    await wrapper.get('[data-dialog] input:not([type])').setValue('Renamed account-wide')
    await wrapper.get('[data-passive-save]').trigger('click'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ model: '', scope: 'account', schema_version: 2 }))
    wrapper.unmount()
  })

  it('keeps legacy clearing behavior and does not enable automatic model isolation', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 8, version: 1, name: 'Legacy', group_ids: [8], schema_version: 1, model: 'A', scope: 'model' }])
    const wrapper = makeWrapper(); await flushPromises()
    await wrapper.findAll('button').find(b => b.text() === 'common.edit')!.trigger('click')
    await wrapper.get('input[list="passive-models"]').setValue('')
    const scope = wrapper.get('[data-dialog]').findAll('select').find(s => s.find('option[value="account"]').exists())!
    expect((scope.element as HTMLSelectElement).value).toBe('account')
    expect(scope.attributes('disabled')).toBeDefined()
    wrapper.unmount()
  })

  it('renders separate models on the same account and targets actions to the selected row', async () => {
    const base = { policy_id: 1, policy_name: 'Models', account_id: 62, scope: 'model', mode: 'enforce', phase: 'paused', reason: 'insufficient_samples', used: 0, inflight: 0, other_hold: false, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, complete: true } }
    const a = { ...base, model: 'model-a' }, b = { ...base, model: 'model-b' }
    api.states.mockResolvedValue([a, b])
    const wrapper = makeWrapper('records'); await flushPromises()
    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('model-a'); expect(rows[1].text()).toContain('model-b')
    await rows[1].findAll('button').find(button => button.text() === 'Verify now')!.trigger('click'); await flushPromises()
    expect(api.act).toHaveBeenCalledWith(b, 'verify', 0)
    await wrapper.findAll('tbody tr')[0].findAll('button').find(button => button.text() === 'Temporary exemption')!.trigger('click')
    expect(wrapper.get('[data-dialog]').text()).toContain('only model “model-a”')
    await wrapper.get('[data-dialog]').findAll('button').find(button => button.text() === 'common.confirm')!.trigger('click'); await flushPromises()
    expect(api.act).toHaveBeenLastCalledWith(a, 'exempt', 1800)
    wrapper.unmount()
  })

  it('uses the independent summary for cards and shows the server state total beyond the loaded page', async () => {
    api.stateSummary.mockResolvedValue({ ...emptyPassiveSummary(), total: 250, paused_accounts: 90, limited_accounts: 80, trial_accounts: 30, observed_accounts: 50 })
    api.statePage.mockResolvedValue({ items: [recordState()], total: 250, has_more: true, next_cursor: 'state-page-2' })
    const wrapper = makeWrapper('records'); await flushPromises()
    expect(wrapper.findAll('button').find(button => button.text().includes('Paused accounts'))?.text()).toContain('90')
    expect(wrapper.get('[data-more-states]').exists()).toBe(true)
    expect(wrapper.text()).toContain('Loaded 1 of 250 states')
    wrapper.unmount()
  })

  it('does not refresh or replace rows while a later state page is being appended', async () => {
    vi.useFakeTimers()
    api.statePage.mockResolvedValueOnce({ items: [recordState()], total: 2, has_more: true, next_cursor: 'state-page-2' })
    let resolveNext!: (value: { items: ReturnType<typeof recordState>[]; total: number; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveNext = resolve }))
    const wrapper = makeWrapper('records'); await flushPromises()
    await wrapper.get('[data-more-states]').trigger('click')
    expect(wrapper.findAll('tbody tr').map(row => row.text()).join(' ')).toContain('Account A')
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.statePage).toHaveBeenCalledTimes(2)
    resolveNext({ items: [recordState({ account_id: 63, account_name: 'Account B', model: 'model-b' })], total: 2, has_more: false })
    await flushPromises()
    expect(wrapper.findAll('tbody tr').map(row => row.text()).join(' ')).toContain('Account A')
    expect(wrapper.findAll('tbody tr').map(row => row.text()).join(' ')).toContain('Account B')
    wrapper.unmount()
  })

  it('clears a prior state page on filter change and ignores its late append response', async () => {
    api.statePage.mockResolvedValueOnce({ items: [recordState({ account_name: 'Old account' })], total: 2, has_more: true, next_cursor: 'state-page-2' })
    let resolveOldAppend!: (value: { items: ReturnType<typeof recordState>[]; total: number; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveOldAppend = resolve }))
    let resolveFiltered!: (value: { items: ReturnType<typeof recordState>[]; total: number; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveFiltered = resolve }))
    const wrapper = makeWrapper('records'); await flushPromises()
    await wrapper.get('[data-more-states]').trigger('click')
    await wrapper.get('[data-record-phase]').setValue('paused'); await flushPromises()
    expect(wrapper.findAll('tbody tr').map(row => row.text()).join(' ')).not.toContain('Old account')
    expect(wrapper.find('[data-more-states]').exists()).toBe(false)
    resolveFiltered({ items: [recordState({ account_id: 63, account_name: 'Filtered account', phase: 'paused' })], total: 1, has_more: false })
    await flushPromises()
    expect(wrapper.findAll('tbody tr').map(row => row.text()).join(' ')).toContain('Filtered account')
    expect(wrapper.findAll('tbody tr').map(row => row.text()).join(' ')).not.toContain('Old account')
    resolveOldAppend({ items: [recordState({ account_id: 64, account_name: 'Stale account' })], total: 2, has_more: false })
    await flushPromises()
    const rows = wrapper.findAll('tbody tr').map(row => row.text()).join(' ')
    expect(rows).toContain('Filtered account')
    expect(rows).not.toContain('Old account')
    expect(rows).not.toContain('Stale account')
    wrapper.unmount()
  })

  it('replaces previously appended states when background refresh discovers a new policy version', async () => {
    vi.useFakeTimers()
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 1 }])
    api.statePage.mockResolvedValueOnce({ items: [recordState()], total: 2, has_more: true, next_cursor: 'state-page-2' })
    api.statePage.mockResolvedValueOnce({ items: [recordState({ account_id: 63, account_name: 'Old policy page' })], total: 2, has_more: false })
    api.statePage.mockResolvedValueOnce({ items: [recordState({ account_id: 64, account_name: 'New policy evidence' })], total: 1, has_more: false })
    const wrapper = makeWrapper('records'); await flushPromises()
    await wrapper.get('[data-more-states]').trigger('click'); await flushPromises()
    expect(wrapper.text()).toContain('Old policy page')
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, version: 2 }])
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.statePage).toHaveBeenCalledTimes(3)
    expect(wrapper.findAll('tbody tr')).toHaveLength(1)
    expect(wrapper.text()).toContain('New policy evidence')
    expect(wrapper.text()).not.toContain('Old policy page')
    wrapper.unmount()
  })

  it('sends server-side event filters as ISO times and resets pagination when filters change', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, name: 'Policy' }])
    api.eventPage.mockResolvedValue({ items: [], has_more: false })
    const wrapper = makeWrapper('records'); await flushPromises()
    await wrapper.get('[data-record-policy]').setValue('1')
    await wrapper.get('[data-record-account]').setValue('62')
    await wrapper.get('[data-record-model]').setValue('model-a')
    await wrapper.get('[data-event-kind]').setValue('pause')
    await wrapper.get('[data-event-since]').setValue('2026-10-24T01:30')
    await wrapper.get('[data-event-until]').setValue('2026-10-24T03:30')
    await flushPromises()
    expect(api.eventPage).toHaveBeenLastCalledWith(expect.objectContaining({
      policy_id: 1, account_id: 62, model: 'model-a', kind: 'pause',
      since: new Date('2026-10-24T01:30').toISOString(), until: new Date('2026-10-24T03:30').toISOString(), cursor: undefined,
    }))
    wrapper.unmount()
  })

  it('ignores an event append response after changing the event filter', async () => {
    api.eventPage.mockResolvedValueOnce({ items: [event(1)], has_more: true, next_cursor: 'event-page-2' })
    let resolveOldAppend!: (value: { items: ReturnType<typeof event>[]; has_more: boolean }) => void
    api.eventPage.mockImplementationOnce(() => new Promise(resolve => { resolveOldAppend = resolve }))
    api.eventPage.mockResolvedValueOnce({ items: [event(3, { account_id: 63 })], has_more: false })
    const wrapper = makeWrapper('records'); await flushPromises()
    await wrapper.get('[data-more-events]').trigger('click')
    await wrapper.get('[data-event-kind]').setValue('recovered'); await flushPromises()
    expect(wrapper.findAll('ol li')).toHaveLength(1)
    expect(wrapper.find('ol').text()).toContain('#63')
    resolveOldAppend({ items: [event(2)], has_more: false }); await flushPromises()
    expect(wrapper.findAll('ol li')).toHaveLength(1)
    expect(wrapper.find('ol').text()).toContain('#63')
    expect(wrapper.find('ol').text()).not.toContain('#62')
    wrapper.unmount()
  })

  it('shows zero fresh values and keeps unknown fresh metrics explicit', async () => {
    api.statePage.mockResolvedValue({ items: [recordState({ evidence: { ...recordState().evidence, fresh_error_rate: 0, fresh_ttft: 0, ttft_fresh: 0, fresh_cache_rate: null, cache_fresh: 0 } })], total: 1, has_more: false })
    const wrapper = makeWrapper('records'); await flushPromises()
    const fresh = wrapper.get('[data-fresh-evidence]').text()
    expect(fresh).toContain('errors 0% (0)')
    expect(fresh).toContain('first-token P90 0 ms (0)')
    expect(fresh).toContain('cache — (0)')
    wrapper.unmount()
  })

})
