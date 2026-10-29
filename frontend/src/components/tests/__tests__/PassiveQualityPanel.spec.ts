import { defaultPassivePolicy } from '@/test/fixtures/passiveQuality'
import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import PassiveQualityPanel from '../PassiveQualityPanel.vue'
import { emptyPassiveSummary } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
const api = vi.hoisted(() => ({ policies: vi.fn(), states: vi.fn(), events: vi.fn(), statePage: vi.fn(), stateSummary: vi.fn(), eventPage: vi.fn(), save: vi.fn(), remove: vi.fn(), act: vi.fn(), models: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/admin/passiveQuality', async original => ({ ...await original<object>(), passiveQualityAPI: api }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getModelAllowlistCandidates: api.models } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))
vi.mock('vue-router', () => ({ useRoute: () => ({ query: {} }) }))
const recordState = (overrides: Record<string, unknown> = {}) => ({
  policy_id: 1, policy_name: 'Policy', account_id: 62, account_name: 'Account A', model: 'model-a', scope: 'model', mode: 'enforce',
  phase: 'healthy', reason: '', hits: 0, backoff: 0, used: 0, inflight: 0, generation: 1, next_at: '', trial_started_at: '', trial_ends_at: '', updated_at: '', other_hold: false,
  evidence: { samples: 20, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, fresh: 0, complete: true, latest: '' },
  ...overrides,
})
const event = (id: number, overrides: Record<string, unknown> = {}) => ({ id, policy_id: 1, account_id: 62, policy_name: 'Policy', kind: 'pause', detail: recordState(), created_at: '2026-10-24T00:00:00Z', ...overrides })
const makeWrapper = () => mount(PassiveQualityPanel, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
  stubs: { BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' } },
} })
beforeEach(() => { vi.resetAllMocks(); api.policies.mockResolvedValue([]); api.states.mockResolvedValue([]); api.events.mockResolvedValue([]); api.statePage.mockImplementation(async () => ({ items: await api.states(), total: (await api.states()).length, has_more: false })); api.eventPage.mockImplementation(async () => ({ items: await api.events(), has_more: false })); api.stateSummary.mockResolvedValue(emptyPassiveSummary()); api.models.mockResolvedValue(['model']); api.save.mockResolvedValue({}) })
afterEach(() => vi.useRealTimers())
describe('passive quality records', () => {
  it('labels group target records as actual cache and starts a real-traffic trial', async () => {
    const state = { purpose: 'group_quality_target', policy_id: 7, policy_name: 'Group targets', account_id: 62, model: 'A', scope: 'model', mode: 'enforce', phase: 'paused', reason: 'group_target_trial', used: 0, inflight: 0, other_hold: false, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: 1000, ttft_samples: 10, cache_rate: 0, cache_samples: 10, complete: true } }
    api.states.mockResolvedValue([state])
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.text()).toContain('Group quality protection')
    expect(wrapper.text()).toContain('Actual cache usage: 0%')
    expect(wrapper.text()).toContain('Availability: 80%')
    await wrapper.findAll('button').find(button => button.text() === 'Start recovery trial')!.trigger('click'); await flushPromises()
    expect(api.act).toHaveBeenCalledWith(state, 'verify', 0)
    wrapper.unmount()
  })
  it('requests an active check without opening customer trials and renders independent active evidence', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, purpose: 'group_quality_target', recovery_mode: 'active' }])
    const state = recordState({ purpose: 'group_quality_target', phase: 'paused', active_recovery: {
      verdict: 'pending', passed: false, requests: 5, warmup_requests: 1, batches: 1,
      started_at: '2026-10-24T00:00:00Z', finished_at: '2026-10-24T00:00:10Z', reason: 'insufficient_active_recovery_batches',
      evidence: { ...recordState().evidence, error_rate: 0, cache_rate: 0, cache_samples: 5, output_tps: null },
    } })
    api.states.mockResolvedValue([state])
    api.events.mockResolvedValue([event(1, { detail: state, kind: 'active_recovery_pending' })])
    const w = makeWrapper(); await flushPromises()
    expect(w.findAll('[data-active-recovery-evidence]')).toHaveLength(2)
    expect(w.get('tbody [data-active-recovery-evidence]').text()).toContain('Actual cache usage: 0%')
    expect(w.get('tbody [data-active-recovery-evidence]').text()).toContain('Output TPS: Unknown')
    expect(w.get('tbody [data-active-recovery-evidence]').text()).toContain('excluded from the one-hour production metrics')
    await w.findAll('button').find(button => button.text() === 'Run active recovery checks')!.trigger('click'); await flushPromises()
    expect(api.act).toHaveBeenCalledWith(state, 'verify', 0)
    expect(api.success).toHaveBeenCalledWith(expect.stringContaining('no customer trial is opened'))
    expect(w.get('tbody tr').text()).not.toContain('Start recovery trial')
    w.unmount()
  })

  it('uses active probing labels for system recovery', async () => {
    api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 1, purpose: 'group_quality_target', recovery_mode: 'active' }])
    api.states.mockResolvedValue([recordState({ purpose: 'group_quality_target', phase: 'probing' })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('tbody tr').text()).toContain('Running active recovery checks')
    expect(w.get('tbody tr').text()).not.toContain('Preparing real-traffic trial')
    w.unmount()
  })

  it('shows TPS evidence and preserves an independent speed restriction when availability is healthy', async () => {
    api.states.mockResolvedValue([recordState({
      speed: { phase: 'limited', reason: 'output_tps: 8.00 / 10.00 (20)', hits: 2, recovery_hits: 0 },
      evidence: { ...recordState().evidence, output_tps: 8, tps_samples: 20, fresh_output_tps: 9, tps_fresh: 5 },
    })])
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.get('[data-passive-tps]').text()).toBe('Output TPS: 8 token/s (20)')
    expect(wrapper.get('[data-speed-state]').text()).toContain('TPS state')
    expect(wrapper.get('[data-fresh-evidence]').text()).toContain('Output TPS 9 token/s (5)')
    expect(wrapper.get('tbody tr').text()).toContain('Limited')
    wrapper.unmount()
  })

  it('shows missing metrics and keeps verify distinct from temporary exemption', async () => {
    const state = { policy_id: 1, policy_name: 'Policy', account_id: 62, model: '', probe_model: 'observed-model', scope: 'account', mode: 'enforce', phase: 'paused', reason: 'insufficient_metric_samples', used: 0, inflight: 0, other_hold: true, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, complete: true } }
    api.states.mockResolvedValue([state]);const wrapper = makeWrapper();await flushPromises()
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
  it('renders separate models on the same account and targets actions to the selected row', async () => {
    const base = { policy_id: 1, policy_name: 'Models', account_id: 62, scope: 'model', mode: 'enforce', phase: 'paused', reason: 'insufficient_samples', used: 0, inflight: 0, other_hold: false, evidence: { samples: 10, failures: 2, error_rate: 20, ttft: null, ttft_samples: 0, cache_rate: null, cache_samples: 0, complete: true } }
    const a = { ...base, model: 'model-a' }, b = { ...base, model: 'model-b' }
    api.states.mockResolvedValue([a, b])
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
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
    const wrapper = makeWrapper(); await flushPromises()
    const fresh = wrapper.get('[data-fresh-evidence]').text()
    expect(fresh).toContain('errors 0% (0)')
    expect(fresh).toContain('first-token P90 0 ms (0)')
    expect(fresh).toContain('cache — (0)')
    wrapper.unmount()
  })

})
