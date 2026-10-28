import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import GroupQualityTargetsPanel from '../GroupQualityTargetsPanel.vue'
import { defaultPassivePolicy, emptyPassiveSummary, type GroupQualityContributor, type GroupQualityTargetSummary, type GroupQualityTrafficControl, type GroupQualityFallbackSummary, type PassivePolicy, type PassiveState } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import zhResources from '@/i18n/locales/zh/admin/resources'
import type { AdminGroup } from '@/types'

const api = vi.hoisted(() => ({ policies: vi.fn(), states: vi.fn(), statePage: vi.fn(), stateSummary: vi.fn(), groupTargets: vi.fn(), saveGroupQualityTarget: vi.fn(), success: vi.fn(), error: vi.fn() }))
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
  mode: 'enforce', min_samples: 5, window_seconds: 3600, max_samples: 100, confirmations: 2, new_samples: 2,
  cooldown_seconds: 300, max_cooldown_seconds: 1800, trial_seconds: 900, trial_requests: 20, trial_concurrency: 1, recovery_samples: 5,
  error_rate: { enabled: true, pause: 20, recover: 15 }, ttft: { enabled: true, pause: 10000, recover: 8000 }, cache_rate: { enabled: true, pause: 80, recover: 85 },
  cache_protection: undefined, ttft_action: undefined, ttft_concurrency_limit: undefined, ttft_priority_penalty: undefined, ...overrides,
})
const summary = (overrides: Partial<GroupQualityTargetSummary> = {}): GroupQualityTargetSummary => ({
  policy_id: 11, group_id: 3, window_seconds: 3600, actual_cache_rate: 90, cache_samples: 10, cache_read_tokens: 900, cache_input_tokens: 1000, cache_unknown_samples: 0,
  actual_availability: 100, success_samples: 10, failure_samples: 0, failure_unknown_samples: 0, ttft_p90_ms: 1000, ttft_samples: 10,
  generated_at: '2026-10-24T00:00:00Z', telemetry_complete: true, ...overrides,
})
const trafficControl = (overrides: Partial<GroupQualityTrafficControl> = {}): GroupQualityTrafficControl => ({
  enabled: true, mode: 'normal', verified_accounts: 3, validation_input_tokens: 12500, total_input_tokens: 500000,
  validation_share_percent: 2.5, reserved_input_tokens: 5000, budget_tokens: 25000, remaining_tokens: 7500, limited_requests: 12,
  ...overrides,
})
const state = (overrides: Partial<PassiveState> = {}): PassiveState => ({
  policy_id: 11, policy_name: 'Pro target', purpose: 'group_quality_target', account_id: 62, account_name: 'Provider', model: 'model-a', scope: 'model', mode: 'enforce',
  phase: 'trial', reason: 'group_target_trial', hits: 0, backoff: 0, used: 3, inflight: 0, generation: 1,
  next_at: '', trial_started_at: '2026-10-24T00:00:00Z', trial_ends_at: '2026-10-24T00:15:00Z', updated_at: '2026-10-24T00:01:00Z', other_hold: false,
  evidence: { samples: 3, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: 500, ttft_samples: 3, cache_rate: 0, cache_samples: 2, fresh: 2, complete: true, latest: '' },
  ...overrides,
})
const fallback = (overrides: Partial<GroupQualityFallbackSummary> = {}): GroupQualityFallbackSummary => ({
  enabled: true, in_flight: 0, requests: 0, input_tokens: 0, reserved_input_tokens: 0, lease_seconds: 120, pacing_seconds: 0, ...overrides,
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
  it('shows automatic fallback independently of a disabled cache metric', async () => {
    api.policies.mockResolvedValue([policy({ cache_rate: { enabled: false, pause: 80, recover: 85 } })])
    api.groupTargets.mockResolvedValue([summary({ fallback: fallback() })])
    const w = makeWrapper(); await flushPromises()
    expect(w.find('[data-target-traffic-control]').exists()).toBe(false)
    expect(w.get('[data-target-fallback-status]').text()).toBe('Enabled · Standby')
    expect(w.get('[data-target-fallback-inflight] .text-xl').text()).toBe('0')
    expect(w.get('[data-target-fallback]').text()).toContain('Fallback adds no concurrency cap or delay between request starts')
    expect(w.get('[data-target-fallback]').text()).toContain('normal account concurrency limits still apply')
    expect(w.get('[data-target-no-verified]').text()).toContain('scored fallback adds no extra concurrency cap')
    expect(w.find('[data-target-fallback-last]').exists()).toBe(false)
    w.unmount()
  })

  it('distinguishes active fallback from recent history and shows real usage', async () => {
    api.groupTargets.mockResolvedValue([summary({ fallback: fallback({ in_flight: 3, requests: 7, input_tokens: 32000, reserved_input_tokens: 4096, last_used_at: '2026-10-24T01:00:00Z', last_account_id: 62 }) })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-fallback-status]').text()).toBe('Serving fallback traffic')
    expect(w.get('[data-target-fallback-inflight] .text-xl').text()).toBe('3')
    expect(w.get('[data-target-fallback-requests]').text()).toContain('7')
    expect(w.get('[data-target-fallback-input]').text()).toContain('32,000')
    expect(w.get('[data-target-fallback-input]').text()).toContain('4,096')
    expect(w.get('[data-target-fallback-last]').text()).toContain('account #62')
    expect(w.get('[data-target-fallback]').text()).toContain('Manual stops, invalid credentials and hard limits still apply')
    w.unmount()
    api.groupTargets.mockResolvedValue([summary({ fallback: fallback({ requests: 7 }) })])
    const idle = makeWrapper(); await flushPromises()
    expect(idle.get('[data-target-fallback-status]').text()).toBe('Enabled · Standby')
    idle.unmount()
  })

  it('shows uncapped fallback in Chinese even when a legacy pacing field is present', async () => {
    api.groupTargets.mockResolvedValue([summary({ fallback: fallback({ in_flight: 3, pacing_seconds: 2 }) })])
    const w = makeWrapper(undefined, 'zh'); await flushPromises()
    expect(w.get('[data-target-fallback-inflight] .text-xl').text()).toBe('3')
    expect(w.get('[data-target-fallback]').text()).toContain('兜底不额外限制并发或请求启动间隔，仍遵守账号原有并发设置')
    expect(w.get('[data-target-fallback]').text()).not.toContain('最多 1 个')
    expect(w.get('[data-target-fallback]').text()).not.toContain('间隔 2 秒')
    expect(w.get('[data-target-no-verified]').text()).toContain('按评分兜底，不额外限制并发')
    w.unmount()
  })

  it.each([
    { value: undefined, policy: {} },
    { value: fallback({ enabled: false }), policy: {} },
    { value: fallback(), policy: { enabled: false } },
    { value: fallback(), policy: { mode: 'observe' as const } },
  ])('does not invent fallback support or enablement: %j', async values => {
    api.policies.mockResolvedValue([policy(values.policy)])
    api.groupTargets.mockResolvedValue([summary({ fallback: values.value })])
    const w = makeWrapper(); await flushPromises()
    expect(w.find('[data-target-fallback]').exists()).toBe(false)
    w.unmount()
  })

  it('creates Plus with visible 70/70/20 presets and independent recovery thresholds', async () => {
    api.policies.mockResolvedValue([])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('4')
    expect((w.get('[data-target-cache]').element as HTMLInputElement).value).toBe('70')
    expect((w.get('[data-target-availability]').element as HTMLInputElement).value).toBe('70')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('20')
    expect(w.text()).toContain('Cold starts')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ id: 0, purpose: 'group_quality_target', group_ids: [4], mode: 'enforce', enabled: true, model: '',
      cache_rate: { enabled: true, pause: 70, recover: 75 }, error_rate: { enabled: true, pause: 30, recover: 25 }, ttft: { enabled: true, pause: 20000, recover: 16000 } }))
    expect(api.saveGroupQualityTarget.mock.calls[0][0]).not.toHaveProperty('cache_protection')
    expect(api.saveGroupQualityTarget.mock.calls[0][0]).not.toHaveProperty('ttft_action')
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
    expect(w.get('[data-dialog]').text()).toContain('No extra configuration is needed')
    await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ group_ids: [3],
      cache_rate: { enabled: true, pause: 80, recover: 85 }, ttft: { enabled: true, pause: 15000, recover: 12000 } }))
    w.unmount()
  })

  it('keeps other groups and existing targets unchanged, without creating a duplicate', async () => {
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('5')
    expect((w.get('[data-target-cache]').element as HTMLInputElement).value).toBe('80')
    expect((w.get('[data-target-availability]').element as HTMLInputElement).value).toBe('80')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('10')
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
    api.policies.mockResolvedValue([policy({ model: 'only-model', mode: 'observe', excluded_account_ids: [3, 2280, 2283], cooldown_seconds: 600, trial_requests: 30 })])
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-toggle="11"]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenCalledWith(expect.objectContaining({ enabled: false, mode: 'observe', model: 'only-model', excluded_account_ids: [3, 2280, 2283], cooldown_seconds: 600, trial_requests: 30 }))
    await w.get('[data-target-edit="11"]').trigger('click')
    expect(w.get('[data-dialog]').text()).toContain('Existing model filter preserved: only-model')
    expect(w.get('[data-dialog]').text()).toContain('3 account exclusions are preserved')
    await w.get('[data-target-cache]').setValue('85'); await w.get('[data-target-save]').trigger('click'); await flushPromises()
    expect(api.saveGroupQualityTarget).toHaveBeenLastCalledWith(expect.objectContaining({ mode: 'observe', model: 'only-model', excluded_account_ids: [3, 2280, 2283] }))
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
    expect(w.get('[data-target-metric="ttft"]').text()).toContain('Needs 5 more')
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

  it('uses strict target boundaries and keeps account trial evidence separate from the group metric', async () => {
    api.groupTargets.mockResolvedValue([summary({ actual_cache_rate: 80 })]); api.states.mockResolvedValue([state()])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('Target not met')
    expect(w.get('[data-target-metric="cache"]').text()).toContain('> 80%')
    expect(w.get('[data-target-state="62-model-a"]').text()).toContain('Trial attempts 3/20 · 2/5 valid samples')
    expect(w.get('[data-target-state="62-model-a"]').text()).toContain('Actual cache usage: 0%')
    expect(w.get('[data-target-state="62-model-a"]').text()).toContain('verifying recovery with limited fresh production traffic')
    expect(w.get('[data-target-metric="cache"]').text()).toContain('80%')
    w.unmount()
  })

  it('prominently warns when group metrics pass but every account/model is paused', async () => {
    api.states.mockResolvedValue([state({ phase: 'paused' })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('Measured targets met')
    expect(w.get('[data-target-verified]').text()).toContain('0 account/models verified')
    expect(w.get('[data-target-no-verified]').text()).toContain('protection is not bypassed when all are paused')
    w.unmount()
  })

  it('distinguishes deferred recovery from an open trial without claiming another failure', async () => {
    const waiting = state({ reason: 'waiting_for_recovery: insufficient_samples', next_at: '2026-10-24T00:20:00Z' })
    api.states.mockResolvedValue([waiting])
    const w = makeWrapper(); await flushPromises()
    const row = w.get('[data-target-state="62-model-a"]')
    expect(row.text()).toContain('Waiting for recovery evidence')
    expect(row.get('[data-target-trial-waiting]').text()).toContain('Missing evidence is not a new failure')
    expect(row.text()).not.toContain('Trial ends:')
    expect(row.text()).not.toContain('Trial failed again')
    w.unmount()

    api.states.mockResolvedValue([state({ ...waiting, next_at: '0001-01-01T00:00:00Z', trial_ends_at: '2026-10-24T00:35:00Z' })])
    const resumed = makeWrapper(); await flushPromises()
    expect(resumed.find('[data-target-trial-waiting]').exists()).toBe(false)
    expect(resumed.get('[data-target-state="62-model-a"]').text()).toContain('Trial ends:')
    resumed.unmount()
  })

  it('shows the server token budget separately from request and account evidence', async () => {
    api.groupTargets.mockResolvedValue([summary({ traffic_control: trafficControl() })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-traffic-mode]').text()).toBe('Normal validation')
    expect(w.get('[data-target-validation-share]').text()).toContain('2.5%')
    expect(w.get('[data-target-validation-share]').text()).toContain('Validation input 12,500 / group input 500,000 tokens')
    expect(w.get('[data-target-budget-remaining]').text()).toContain('7,500')
    expect(w.get('[data-target-budget-remaining]').text()).toContain('Reserved 5,000 / total budget 25,000 tokens')
    expect(w.get('[data-target-limited-requests]').text()).toContain('12')
    expect(w.get('[data-target-limited-requests]').text()).toContain('Verified account/model pairs: 3')
    expect(w.get('[data-target-verified]').text()).toContain('3 account/models verified')
    expect(w.find('[data-target-no-verified]').exists()).toBe(false)
    expect(w.get('[data-target-traffic-control]').text()).toContain('reservations remain when usage is missing')
    expect(w.get('[data-target-traffic-control]').text()).toContain('does not mean every model has verified supply')
    expect(w.find('[data-target-bootstrap]').exists()).toBe(false)
    w.unmount()
  })

  it('makes bootstrap limits explicit even when the measured group window meets its targets', async () => {
    api.groupTargets.mockResolvedValue([summary({ traffic_control: trafficControl({ mode: 'bootstrap', verified_accounts: 0 }) })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('Measured targets met')
    expect(w.get('[data-target-bootstrap]').attributes('role')).toBe('status')
    expect(w.get('[data-target-bootstrap]').text()).toContain('Only limited validation traffic is admitted; the targets are not guaranteed')
    expect(w.find('[data-target-no-verified]').exists()).toBe(false)
    w.unmount()
  })

  it('shows an exhausted budget as zero and explains conserving mode', async () => {
    api.groupTargets.mockResolvedValue([summary({ traffic_control: trafficControl({ mode: 'conserving', remaining_tokens: 0, validation_share_percent: 0, validation_input_tokens: 0 }) })])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-traffic-mode]').text()).toBe('Conserving validation traffic')
    expect(w.get('[data-target-budget-remaining]').find('.text-xl').text()).toBe('0')
    expect(w.get('[data-target-validation-share]').text()).toContain('0%')
    expect(w.get('[data-target-traffic-control]').text()).toContain('Validation traffic is being reduced')
    expect(w.get('[data-target-traffic-control]').text()).not.toContain('Unknown')
    w.unmount()
  })

  it.each([
    { control: undefined, policy: {} },
    { control: trafficControl({ enabled: false }), policy: {} },
    { control: trafficControl(), policy: { enabled: false } },
    { control: trafficControl(), policy: { mode: 'observe' as const } },
  ])('does not claim an active budget for old servers or inactive enforcement: %j', async values => {
    api.policies.mockResolvedValue([policy(values.policy)])
    api.groupTargets.mockResolvedValue([summary({ traffic_control: values.control })])
    const w = makeWrapper(); await flushPromises()
    expect(w.find('[data-target-traffic-control]').exists()).toBe(false)
    expect(w.get('[data-target-metric="cache"]').text()).toContain('90%')
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
      state({ account_id: 67, phase: 'trial', evidence }),
    ])
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-target-verified]').text()).toContain('1 account/models verified')
    expect(w.find('[data-target-no-verified]').exists()).toBe(false)
    expect(w.get('[data-group-target]').text()).toContain('not guaranteed current availability')
    w.unmount()
  })

  it('keeps independent policy counts and appended state pages through automatic refreshes', async () => {
    vi.useFakeTimers()
    api.stateSummary.mockResolvedValue({ ...emptyPassiveSummary(), total: 51, by_policy: [{ policy_id: 11, total: 51, phases: { paused: 40, trial: 10, healthy: 1 }, verified: 1 }] })
    api.statePage.mockResolvedValueOnce({ items: [state()], total: 51, has_more: true, next_cursor: 'policy-page-2' })
    let resolveNext!: (value: { items: PassiveState[]; total: number; has_more: boolean }) => void
    api.statePage.mockImplementationOnce(() => new Promise(resolve => { resolveNext = resolve }))
    const w = makeWrapper(); await flushPromises()
    expect(w.get('[data-group-target]').text()).toContain('51 account/model states')
    expect(w.get('[data-group-target]').text()).toContain('40 paused')
    expect(w.get('[data-group-target]').text()).toContain('10 in recovery trial')
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
    for (const value of ['Provider · model-a', 'Normal', 'Recovery / validation', 'Fallback', 'Unattributed', '#63', '8,000', '2 / 10', '3 / 10', '1.5 s', 'Unknown', '4 requests without usable cache telemetry', 'Showing 4 of 9 contributors']) expect(text).toContain(value)
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
