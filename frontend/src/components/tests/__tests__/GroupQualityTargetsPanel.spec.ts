import { defaultPassivePolicy } from '@/test/fixtures/passiveQuality'
import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import GroupQualityTargetsPanel from '../GroupQualityTargetsPanel.vue'
import { emptyPassiveSummary, type GroupQualityContributor, type GroupQualityTargetSummary, type PassivePolicy, type PassiveState } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import zhResources from '@/i18n/locales/zh/admin/resources'
import type { AdminGroup } from '@/types'

const api = vi.hoisted(() => ({ policies: vi.fn(), states: vi.fn(), statePage: vi.fn(), stateSummary: vi.fn(), groupTargets: vi.fn(), saveGroupQualityTarget: vi.fn(), remove: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/admin/passiveQuality', async original => ({ ...await original<object>(), passiveQualityAPI: api }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))
const groups = [
  { id: 3, name: 'GPT-Pro', platform: 'openai', status: 'active' },
  { id: 4, name: 'GPT-Plus', platform: 'openai', status: 'active' },
  { id: 5, name: 'Other', platform: 'openai', status: 'active' },
  { id: 6, name: 'Unavailable', platform: 'openai', status: 'disabled' },
] as AdminGroup[]
const policy = (overrides: Partial<PassivePolicy> = {}): PassivePolicy => ({
  ...defaultPassivePolicy(), id: 11, version: 3, purpose: 'group_quality_target', name: 'Pro target', group_ids: [3],
  mode: 'enforce', recovery_mode: 'active', min_samples: 20, window_seconds: 3600, max_samples: 0, confirmations: 2, new_samples: 5,
  cooldown_seconds: 300, max_cooldown_seconds: 1800, trial_seconds: 900, trial_requests: 20, trial_concurrency: 1, recovery_samples: 5,
  error_rate: { enabled: true, pause: 20, recover: 15 }, ttft: { enabled: true, pause: 10000, recover: 8000 }, cache_rate: { enabled: true, pause: 80, recover: 85 },
  cache_protection: undefined, ttft_action: undefined, ttft_concurrency_limit: undefined, ttft_priority_penalty: undefined, ...overrides,
})
const summary = (overrides: Partial<GroupQualityTargetSummary> = {}): GroupQualityTargetSummary => ({
  policy_id: 11, group_id: 3, window_seconds: 3600, actual_cache_rate: 90, cache_samples: 30, cache_read_tokens: 900, cache_input_tokens: 1000, cache_unknown_samples: 0,
  actual_availability: 100, success_samples: 30, failure_samples: 0, failure_unknown_samples: 0, ttft_p90_ms: 1000, ttft_samples: 30,
  generated_at: '2026-10-24T00:00:00Z', telemetry_complete: true, ...overrides,
})
const state = (overrides: Partial<PassiveState> = {}): PassiveState => ({
  policy_id: 11, policy_name: 'Pro target', purpose: 'group_quality_target', account_id: 62, account_name: 'Provider', model: 'model-a', scope: 'model', mode: 'enforce',
  phase: 'probing', reason: 'active_recovery_running', hits: 0, backoff: 0, used: 3, inflight: 0, generation: 1,
  next_at: '', trial_started_at: '2026-10-24T00:00:00Z', trial_ends_at: '2026-10-24T00:15:00Z', updated_at: '2026-10-24T00:01:00Z', other_hold: false,
  evidence: { samples: 3, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: 500, ttft_samples: 3, cache_rate: 0, cache_samples: 2, fresh: 2, complete: true, latest: '' },
  ...overrides,
})
function makeWrapper(policyId?: number, locale = 'en') {
  return mount(GroupQualityTargetsPanel, { props: { groups, visible: true, policyId }, global: {
    plugins: [createI18n({ legacy: false, locale, missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources }, zh: { admin: zhResources } } })],
    stubs: { BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' } },
  } })
}
beforeEach(() => { vi.resetAllMocks(); api.policies.mockResolvedValue([policy()]); api.states.mockResolvedValue([]); api.statePage.mockImplementation(async () => ({ items: await api.states(), total: (await api.states()).length, has_more: false })); api.stateSummary.mockImplementation(async () => { const items: PassiveState[] = await api.states(); const phases: Record<string, number> = {}; for (const s of items) phases[s.phase] = (phases[s.phase] || 0) + 1; return { ...emptyPassiveSummary(), total: items.length, by_policy: [{ policy_id: 11, total: items.length, phases, verified: 0 }] } }); api.groupTargets.mockResolvedValue([summary()]); api.saveGroupQualityTarget.mockResolvedValue(policy()) })
afterEach(() => vi.useRealTimers())

describe('simple group quality targets', () => {
  it.each([
    { locale: 'en', warning: 'Other policies and manual restrictions still apply.' },
    { locale: 'zh', warning: '其他策略和人工限制仍然生效。' },
  ])('names the group and preserves the target when deletion is cancelled: $locale', async ({ locale, warning }) => {
    const w = makeWrapper(undefined, locale); await flushPromises()
    await w.get('[data-target-delete="11"]').trigger('click')
    expect(w.get('[data-target-delete-dialog]').text()).toContain('GPT-Pro')
    expect(w.get('[data-target-delete-dialog]').text()).toContain(warning)
    expect(api.remove).not.toHaveBeenCalled()
    await w.get('[data-target-delete-cancel]').trigger('click')
    expect(w.find('[data-target-delete-dialog]').exists()).toBe(false)
    expect(w.find('[data-group-target="11"]').exists()).toBe(true)
    expect(api.remove).not.toHaveBeenCalled()
    w.unmount()
  })

  it('deletes the confirmed target and closes its open editor without affecting other targets', async () => {
    const other = policy({ id: 12, name: 'Plus target', group_ids: [4] })
    api.policies.mockResolvedValue([policy(), other])
    const w = makeWrapper(11); await flushPromises()
    expect(w.find('[data-target-group]').exists()).toBe(true)
    await w.get('[data-target-delete="11"]').trigger('click')
    api.policies.mockResolvedValue([other])
    await w.get('[data-target-delete-confirm]').trigger('click'); await flushPromises()
    expect(api.remove).toHaveBeenCalledTimes(1)
    expect(api.remove).toHaveBeenCalledWith(11)
    expect(api.policies).toHaveBeenCalledTimes(2)
    expect(w.find('[data-group-target="11"]').exists()).toBe(false)
    expect(w.find('[data-group-target="12"]').exists()).toBe(true)
    expect(w.find('[data-target-group]').exists()).toBe(false)
    expect(w.find('[data-target-delete-dialog]').exists()).toBe(false)
    expect(api.success).toHaveBeenCalledWith('Group quality targets deleted')
    w.unmount()
  })

  it('keeps the target and editor available when deletion fails', async () => {
    api.remove.mockRejectedValue(new Error('Deletion unavailable'))
    const w = makeWrapper(11); await flushPromises()
    await w.get('[data-target-delete="11"]').trigger('click')
    await w.get('[data-target-delete-confirm]').trigger('click'); await flushPromises()
    expect(api.error).toHaveBeenCalledWith('Deletion unavailable')
    expect(api.success).not.toHaveBeenCalled()
    expect(w.find('[data-group-target="11"]').exists()).toBe(true)
    expect(w.find('[data-target-group]').exists()).toBe(true)
    expect(w.find('[data-target-delete-dialog]').exists()).toBe(true)
    expect(w.get('[data-target-delete-confirm]').attributes('disabled')).toBeUndefined()
    w.unmount()
  })

  it('submits one deletion while pending and ignores old list and evidence responses after success', async () => {
    api.statePage.mockResolvedValueOnce({ items: [state()], total: 2, has_more: true, next_cursor: 'page-2' })
    let resolveEvidence!: (value: { items: PassiveState[]; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveEvidence = resolve }))
    let resolveDelete!: () => void
    api.remove.mockImplementation(() => new Promise<void>(resolve => { resolveDelete = resolve }))
    const w = makeWrapper(); await flushPromises()
    await w.get('[data-target-more-states="11"]').trigger('click')
    await w.get('[data-target-delete="11"]').trigger('click')
    const confirm = w.get('[data-target-delete-confirm]')
    await Promise.all([confirm.trigger('click'), confirm.trigger('click')])
    expect(api.remove).toHaveBeenCalledTimes(1)
    expect(api.remove).toHaveBeenCalledWith(11)
    expect(confirm.attributes('disabled')).toBeDefined()
    expect(w.get('[data-target-delete-cancel]').attributes('disabled')).toBeDefined()
    let resolveOldList!: (value: PassivePolicy[]) => void
    api.policies.mockImplementationOnce(() => new Promise(resolve => { resolveOldList = resolve }))
    const oldRefresh = w.vm.refresh()
    api.policies.mockRejectedValueOnce(new Error('Refresh unavailable'))
    resolveDelete(); await flushPromises()
    expect(w.find('[data-group-target]').exists()).toBe(false)
    expect(w.get('[role="alert"]').text()).toContain('Refresh unavailable')
    resolveOldList([policy()])
    resolveEvidence({ items: [state({ account_name: 'Stale evidence' })], has_more: false })
    await oldRefresh; await flushPromises()
    expect(w.find('[data-group-target]').exists()).toBe(false)
    expect(w.find('[data-target-state]').exists()).toBe(false)
    expect(api.success).toHaveBeenCalledWith('Group quality targets deleted')
    w.unmount()
  })

  it('does not render retired fallback or customer validation controls even when audit fields exist', async () => {
    api.groupTargets.mockResolvedValue([{ ...summary(), fallback: { enabled: true, in_flight: 3 }, traffic_control: { enabled: true, mode: 'availability' } }])
    const w = makeWrapper(); await flushPromises()
    expect(w.find('[data-target-fallback]').exists()).toBe(false)
    expect(w.find('[data-target-fallback-active]').exists()).toBe(false)
    expect(w.find('[data-target-traffic-details]').exists()).toBe(false)
    expect(w.get('[data-target-full-window]').text()).toContain('no request-count cap')
    w.unmount()
  })

  it('creates every group with 80/90/30/15 targets and independent recovery thresholds', async () => {
    api.policies.mockResolvedValue([])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('4')
    expect((w.get('[data-target-cache]').element as HTMLInputElement).value).toBe('80')
    expect((w.get('[data-target-availability]').element as HTMLInputElement).value).toBe('90')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('15')
    expect(w.text()).toContain('Cold starts')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ id: 0, purpose: 'group_quality_target', recovery_mode: 'active', window_seconds: 3600, max_samples: 0, min_samples: 20, new_samples: 5, confirmations: 2, group_ids: [4], mode: 'enforce', enabled: true, model: '',
      cache_rate: { enabled: true, pause: 80, recover: 85 }, error_rate: { enabled: true, pause: 10, recover: 5 }, ttft: { enabled: true, pause: 15000, recover: 12000 }, tps: { enabled: true, pause: 30, recover: 36 }, stability: { availability_pause_seconds: 60, performance_pause_seconds: 300, recovery_seconds: 120 } }))
    expect(api.saveGroupQualityTarget.mock.calls[0][0]).not.toHaveProperty('cache_protection')
    expect(api.saveGroupQualityTarget.mock.calls[0][0]).not.toHaveProperty('ttft_action')
    w.unmount()
  })

  it('keeps new targets minimal and makes full-hour active recovery rules visible', async () => {
    api.policies.mockResolvedValue([])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    const rules = w.get('[data-active-recovery-settings]')
    expect(rules.text()).toContain('per account and model')
    expect(rules.text()).toContain('no request-count cap')
    expect(rules.text()).toContain('20 valid samples')
    expect(rules.text()).toContain('including emergency fallback')
    expect(w.find('[data-target-traffic-mode-select]').exists()).toBe(false)
    expect(w.find('[data-target-performance-debounce]').exists()).toBe(false)
    expect(w.text()).toContain('Customer traffic is not used for recovery trials')
    w.unmount()
  })

  it('shows active recovery separately and hides customer trials and emergency fallback for active targets', async () => {
    api.policies.mockResolvedValue([policy({ recovery_mode: 'active', min_samples: 20, max_samples: 0, new_samples: 5 })])
    api.groupTargets.mockResolvedValue([summary()])
    api.states.mockResolvedValue([state({ phase: 'probing', reason: 'active_recovery_started', active_recovery: {
      verdict: 'pending', passed: false, requests: 6, warmup_requests: 1, batches: 1,
      started_at: '2026-10-24T00:02:00Z', finished_at: '2026-10-24T00:02:01Z', reason: 'insufficient_active_recovery_batches',
      evidence: { ...state().evidence, samples: 5, failures: 0, error_rate: 0, cache_rate: 90, cache_samples: 5 },
    } })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-full-window]').text()).toContain('no request-count cap')
    expect(w.get('[data-target-state]').text()).toContain('Running active recovery checks')
    expect(w.get('[data-target-state]').text()).not.toContain('Trial attempts')
    expect(w.get('[data-active-recovery-evidence]').text()).toContain('Actual cache usage: 90%')
    expect(w.get('[data-target-state]').text()).toContain('Actual cache usage: 0%')
    expect(w.find('[data-target-traffic-details]').exists()).toBe(false)
    expect(w.find('[data-target-fallback-active]').exists()).toBe(false)
    w.unmount()
  })

  it('edits existing targets with only active recovery settings and keeps independent exclusions', async () => {
    api.policies.mockResolvedValue([policy({ excluded_account_ids: [62] })])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-edit]').trigger('click')
    expect(w.find('[data-target-traffic-mode-select]').exists()).toBe(false)
    expect(w.find('[data-target-new-session-share]').exists()).toBe(false)
    expect(w.get('[data-active-recovery-settings]').text()).toContain('20 valid samples')
    expect(w.text()).toContain('1 account exclusions are preserved')
    await w.get('[data-target-cache]').setValue('82')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    const saved = api.saveGroupQualityTarget.mock.calls[0][0]
    expect(saved).toMatchObject({ recovery_mode: 'active', window_seconds: 3600, max_samples: 0, excluded_account_ids: [62] })
    for (const field of ['traffic_mode', 'trial_seconds', 'trial_requests', 'trial_concurrency', 'recovery_samples']) expect(saved).not.toHaveProperty(field)
    expect(saved.stability).not.toHaveProperty('new_session_percent')
    w.unmount()
  })

  it('shows evidence timestamps and active recovery separately from new breach confirmations', async () => {
    api.states.mockResolvedValue([state({ phase: 'suspect', reason: 'waiting_for_recovery: stability_confirmation', recovery_since: '2026-10-24T00:00:30Z' }), state({ model: 'model-b', phase: 'suspect', suspicion_since: '2026-10-24T00:00:00Z', reason: 'waiting_for_fresh_failure: cache_rate: 50 / 80 (5)' })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-recovery-since]').text()).toContain('Continuous healthy recovery evidence since')
    expect(w.get('[data-target-suspicion-since]').text()).toContain('Continuous breach evidence since')
    expect(w.text()).toContain('Waiting for sustained healthy evidence and fresh confirmations')
    expect(w.text()).toContain('Old window remains below target; waiting for new failing evidence')
    expect(w.text()).not.toContain('Reduced new assignments')
    w.unmount()
  })

  it('saves an output TPS-only target with a higher recovery standard', async () => {
    api.policies.mockResolvedValue([])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('3')
    for (const metric of ['cache', 'availability', 'ttft']) await w.get(`[data-target-${metric}-enabled]`).setValue(false)
    await w.get('[data-target-tps-enabled]').setValue(true)
    await w.get('[data-target-tps]').setValue(30)
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ tps: { enabled: true, pause: 30, recover: 36 } }))
    w.unmount()
  })

  it('creates Pro with a 15-second first-token target and preserves the simple automatic defaults', async () => {
    api.policies.mockResolvedValue([])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('3')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('15')
    expect(w.get('[data-dialog]').text()).toContain('Customer traffic is not used for recovery trials')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ group_ids: [3],
      cache_rate: { enabled: true, pause: 80, recover: 85 }, ttft: { enabled: true, pause: 15000, recover: 12000 } }))
    w.unmount()
  })

  it('keeps other groups and existing targets unchanged, without creating a duplicate', async () => {
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('5')
    expect((w.get('[data-target-cache]').element as HTMLInputElement).value).toBe('80')
    expect((w.get('[data-target-availability]').element as HTMLInputElement).value).toBe('90')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('15')
    await w.get('[data-target-group]').setValue('3')
    expect(w.get('[data-dialog]').text()).toContain('updates the existing policy')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('10')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).not.toHaveBeenCalled()
    await w.get('[data-target-edit="11"]').trigger('click'); await w.get('[data-target-cache]').setValue('82')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ id: 11, version: 3, cache_rate: { enabled: true, pause: 82, recover: 87 } }))
    w.unmount()
  })

  it('preserves exclusions, model, observation mode and custom recovery parameters when toggled or edited', async () => {
    const stability = { availability_pause_seconds: 0, performance_pause_seconds: 600, recovery_seconds: 180 }
    api.policies.mockResolvedValue([policy({ model: 'only-model', mode: 'observe', excluded_account_ids: [3, 2280, 2283], cooldown_seconds: 600, stability: { ...stability } })])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-toggle="11"]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, mode: 'observe', model: 'only-model', excluded_account_ids: [3, 2280, 2283], cooldown_seconds: 600, stability }))
    expect(api.saveGroupQualityTarget.mock.calls[0][0].stability).not.toHaveProperty('new_session_percent')
    await w.get('[data-target-edit="11"]').trigger('click')
    expect(w.get('[data-dialog]').text()).toContain('Existing model filter preserved: only-model')
    expect(w.get('[data-dialog]').text()).toContain('3 account exclusions are preserved')
    await w.get('[data-target-cache]').setValue('85'); await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenLastCalledWith(expect.objectContaining({ mode: 'observe', model: 'only-model', excluded_account_ids: [3, 2280, 2283], stability }))
    w.unmount()
  })

  it('keeps the reviewed version when a background refresh discovers a newer policy', async () => {
    vi.useFakeTimers()
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-edit="11"]').trigger('click')
    await w.get('[data-target-cache]').setValue('82')
    api.policies.mockResolvedValue([policy({ version: 4, cache_rate: { enabled: true, pause: 90, recover: 95 } })])
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ id: 11, version: 3, cache_rate: { enabled: true, pause: 82, recover: 87 } }))
    w.unmount()
  })

  it('shows a measured zero separately from missing telemetry and never marks missing samples healthy', async () => {
    api.groupTargets.mockResolvedValue([summary({ actual_cache_rate: 0, actual_availability: null, success_samples: 0, ttft_p90_ms: null, ttft_samples: 0 })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-metric="cache"]').text()).toContain('0%')
    expect(w.get('[data-target-metric="availability"]').text()).toContain('Unknown')
    expect(w.get('[data-target-metric="ttft"]').text()).toContain('Needs 20 more')
    expect(w.get('[data-group-target]').text()).toContain('Target not met')
    expect(w.text()).not.toContain('Measured targets met')
    w.unmount()
  })

  it.each([
    { telemetry_complete: false },
    { cache_unknown_samples: 3 },
    { failure_unknown_samples: 1 },
    { cache_samples: 2 },
    { actual_cache_rate: null },
  ])('does not report healthy group targets when evidence is incomplete: %j', async values => {
    api.groupTargets.mockResolvedValue([summary(values)])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('Insufficient samples')
    expect(w.text()).not.toContain('Measured targets met')
    w.unmount()
  })

  it('uses strict target boundaries and keeps account active evidence separate from the group metric', async () => {
    api.groupTargets.mockResolvedValue([summary({ actual_cache_rate: 80 })]); api.states.mockResolvedValue([state()])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('Some accounts protected')
    expect(w.get('[data-target-metric="cache"]').text()).toContain('≥ 80%')
    expect(w.get('[data-target-state="62-model-a"]').text()).toContain('Running active recovery checks')
    expect(w.get('[data-target-state="62-model-a"]').text()).toContain('Actual cache usage: 0%')
    expect(w.get('[data-target-state="62-model-a"]').text()).toContain('Active recovery checks are running')
    expect(w.get('[data-target-metric="cache"]').text()).toContain('80%')
    w.unmount()
  })

  it('prominently warns when group metrics pass but every account/model is paused', async () => {
    api.states.mockResolvedValue([state({ phase: 'paused' })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('Some accounts protected')
    expect(w.get('[data-target-verified]').text()).toContain('0 account/models verified')
    expect(w.get('[data-target-no-verified]').text()).toContain('paused accounts still require recovery verification')
    w.unmount()
  })

  it('keeps deferred active recovery paused and shows its next attempt without customer trials', async () => {
    api.states.mockResolvedValue([state({ phase: 'paused', reason: 'active_recovery_pending: insufficient_metric_samples', next_at: '2026-10-24T00:20:00Z' })])
    const w = makeWrapper(); await flushPromises()
    const row = w.get('[data-target-state="62-model-a"]')
    expect(row.text()).toContain('Waiting for active recovery checks')
    expect(row.text()).toContain('Next active check:')
    expect(row.text()).not.toContain('Trial attempts')
    expect(row.text()).not.toContain('Trial ends:')
    w.unmount()
  })

  it('uses independently aggregated verified counts rather than counting the loaded state page', async () => {
    api.stateSummary.mockResolvedValue({ ...emptyPassiveSummary(), total: 101, by_policy: [{ policy_id: 11, total: 101, phases: { healthy: 1, paused: 100 }, verified: 1 }] })
    const evidence = { ...state().evidence, samples: 10, cache_samples: 10, ttft_samples: 10, cache_rate: 90 }
    api.states.mockResolvedValue([
      state({ phase: 'healthy', evidence }),
      state({ account_id: 63, phase: 'healthy', evidence: { ...evidence, complete: false } }),
      state({ account_id: 64, phase: 'healthy', evidence: { ...evidence, cache_samples: 4 } }),
      state({ account_id: 65, phase: 'healthy', evidence, other_hold: true }),
      state({ account_id: 66, phase: 'healthy', evidence: { ...evidence, cache_rate: 80 } }),
      state({ account_id: 67, phase: 'probing', evidence }),
    ])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-verified]').text()).toContain('1 account/models verified')
    expect(w.find('[data-target-no-verified]').exists()).toBe(false)
    expect(w.get('[data-group-target]').text()).toContain('not guaranteed current availability')
    w.unmount()
  })

  it('keeps independent policy counts and appended state pages through automatic refreshes', async () => {
    vi.useFakeTimers()
    api.stateSummary.mockResolvedValue({ ...emptyPassiveSummary(), total: 51, by_policy: [{ policy_id: 11, total: 51, phases: { paused: 40, probing: 10, healthy: 1 }, verified: 1 }] })
    api.statePage.mockResolvedValueOnce({ items: [state()], total: 51, has_more: true, next_cursor: 'policy-page-2' })
    let resolveNext!: (value: { items: PassiveState[]; total: number; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveNext = resolve }))
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('51 account/model states')
    expect(w.get('[data-group-target]').text()).toContain('50 paused')
    expect(w.text()).toContain('Loaded 1 of 51 states')
    await w.get('[data-target-more-states="11"]').trigger('click')
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.statePage).toHaveBeenCalledTimes(2)
    resolveNext({ items: [state({ account_id: 63, account_name: 'Second account', model: 'model-b' })], total: 51, has_more: false })
    await flushPromises()
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.statePage).toHaveBeenCalledTimes(2)
    expect(w.findAll('[data-target-state]')).toHaveLength(2)
    expect(w.text()).toContain('Loaded 2 of 51 states')
    expect(w.text()).toContain('Automatic refresh is paused')
    w.unmount()
  })

  it('invalidates appended states and their pending responses when the policy version changes', async () => {
    api.statePage.mockResolvedValueOnce({ items: [state({ account_name: 'Old evidence' })], total: 2, has_more: true, next_cursor: 'old-page-2' })
    let resolveOldAppend!: (value: { items: PassiveState[]; total: number; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveOldAppend = resolve }))
    api.statePage.mockResolvedValueOnce({ items: [state({ account_id: 64, account_name: 'New policy evidence', model: 'model-new' })], total: 1, has_more: false })
    const w = makeWrapper(); await flushPromises()
    await w.get('[data-target-more-states="11"]').trigger('click')
    api.policies.mockResolvedValue([policy({ version: 4 })])
    await w.vm.refresh(); await flushPromises()
    expect(w.findAll('[data-target-state]').map(row => row.text()).join(' ')).toContain('New policy evidence')
    expect(w.findAll('[data-target-state]').map(row => row.text()).join(' ')).not.toContain('Old evidence')
    resolveOldAppend({ items: [state({ account_id: 63, account_name: 'Stale append' })], total: 2, has_more: false }); await flushPromises()
    expect(w.findAll('[data-target-state]')).toHaveLength(1)
    expect(w.findAll('[data-target-state]').map(row => row.text()).join(' ')).not.toContain('Stale append')
    expect(w.find('[data-target-more-states]').exists()).toBe(false)
    w.unmount()
  })

  it('renders contributor purposes, unknown measurements and truncation independently of state rows', async () => {
    const contributor = (traffic_kind: GroupQualityContributor['traffic_kind'], overrides: Partial<GroupQualityContributor> = {}): GroupQualityContributor => ({
      account_id: 62, account_name: 'Provider', model: 'model-a', traffic_kind,
      success_samples: 8, failure_samples: 2, cache_samples: 10, cache_unknown_samples: 0,
      cache_input_tokens: 10000, cache_read_tokens: 2000, uncached_tokens: 8000, ttft_samples: 10, slow_requests: 3, ttft_p90_ms: 1500, ...overrides,
    })
    api.groupTargets.mockResolvedValue([summary({
      window_started_at: '2026-10-23T23:00:00Z', contributors: [contributor('normal'), contributor('validation'), contributor('fallback'), contributor('unattributed', { account_id: 63, account_name: '', model: '', cache_unknown_samples: 4, ttft_p90_ms: null })],
      contributor_total: 9, contributors_truncated: true,
    })])
    const w = makeWrapper(); await flushPromises()
    const contributors = w.get('[data-target-contributors]')
    expect(contributors.findAll('tbody tr')).toHaveLength(4)
    const text = contributors.text()
    for (const value of ['Provider · model-a', 'Normal', 'Historical customer validation', 'Historical emergency fallback', 'Unattributed', '#63', '8,000', '2 / 10', '3 / 10', '1.5 s', 'Unknown', '4 requests without usable cache telemetry', 'Showing 4 of 9 contributors']) expect(text).toContain(value)
    expect(w.findAll('[data-target-state]')).toHaveLength(0)
    w.unmount()
  })

  it.each([
    { values: { snapshot_ready: false }, message: 'Waiting for the first reporting snapshot' },
    { values: { stale: true }, message: 'The reporting snapshot is stale' },
  ])('does not report current target success for pending or stale snapshots: %j', async ({ values, message }) => {
    api.groupTargets.mockResolvedValue([summary(values)])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-snapshot]').text()).toContain(message)
    expect(w.get('[data-group-target]').text()).toContain('Insufficient samples')
    expect(w.get('[data-group-target]').text()).not.toContain('Measured targets met')
    w.unmount()
  })

  it.each([{ enabled: false }, { mode: 'observe' as const }])('omits the enforced-pool warning for inactive protection: %j', async values => {
    api.policies.mockResolvedValue([policy(values)])
    const w = makeWrapper(); await flushPromises()
    expect(w.find('[data-target-no-verified]').exists()).toBe(false)
    expect(w.find('[data-target-verified]').exists()).toBe(false)
    w.unmount()
  })

  it('retains targets on summary failure, clears stale metrics, and allows retry', async () => {
    api.groupTargets.mockRejectedValue(new Error('unavailable'))
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').exists()).toBe(true)
    expect(w.get('[role="alert"]').text()).toContain('Current group metrics are unavailable')
    expect(w.get('[data-target-metric="cache"]').text()).toContain('Unknown')
    api.groupTargets.mockResolvedValue([summary()])
    await w.get('[role="alert"] button').trigger('click'); await flushPromises()
    expect(w.find('[role="alert"]').exists()).toBe(false)
    expect(w.get('[data-group-target]').text()).toContain('Measured targets met')
    w.unmount()
  })

  it('rejects missing, out-of-range and entirely disabled metrics before writing', async () => {
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-edit="11"]').trigger('click')
    for (const value of ['', '100', '-1']) {
      await w.get('[data-target-cache]').setValue(value)
      expect(w.get('[data-target-save]').attributes('disabled')).toBeDefined()
    }
    await w.get('[data-target-cache]').setValue('80')
    await w.get('[data-target-ttft]').setValue('0'); expect(w.get('[data-target-save]').attributes('disabled')).toBeDefined()
    await w.get('[data-target-ttft]').setValue('10')
    for (const metric of ['cache', 'availability', 'ttft']) await w.get(`[data-target-${metric}-enabled]`).setValue(false)
    expect(w.get('[data-target-save]').attributes('disabled')).toBeDefined()
    await w.get('[data-target-save]').trigger('click'); expect(api.saveGroupQualityTarget).not.toHaveBeenCalled()
    w.unmount()
  })

  it('preserves legacy cache-only choices and opens capability checks through the existing plan editor', async () => {
    api.policies.mockResolvedValue([policy({ purpose: 'cache_target', error_rate: { enabled: false, pause: 20, recover: 5 }, ttft: { enabled: false, pause: 10000, recover: 5000 } })])
    const w = makeWrapper(11); await flushPromises()
    expect((w.get('[data-target-availability-enabled]').element as HTMLInputElement).checked).toBe(false)
    expect((w.get('[data-target-ttft-enabled]').element as HTMLInputElement).checked).toBe(false)
    await w.get('[data-target-capability]').trigger('click')
    expect(w.emitted('navigate')).toEqual([['plans']])
    w.unmount()
  })
})
