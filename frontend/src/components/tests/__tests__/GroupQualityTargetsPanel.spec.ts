import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import GroupQualityTargetsPanel from '../GroupQualityTargetsPanel.vue'
import { defaultPassivePolicy, type GroupQualityTargetSummary, type PassivePolicy, type PassiveState } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'
import type { AdminGroup } from '@/types'

const api = vi.hoisted(() => ({ policies: vi.fn(), states: vi.fn(), groupTargets: vi.fn(), saveGroupQualityTarget: vi.fn(), success: vi.fn(), error: vi.fn() }))
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
const state = (overrides: Partial<PassiveState> = {}): PassiveState => ({
  policy_id: 11, policy_name: 'Pro target', purpose: 'group_quality_target', account_id: 62, account_name: 'Provider', model: 'model-a', scope: 'model', mode: 'enforce',
  phase: 'trial', reason: 'group_target_trial', hits: 0, backoff: 0, used: 3, inflight: 0, generation: 1,
  next_at: '', trial_started_at: '2026-10-24T00:00:00Z', trial_ends_at: '2026-10-24T00:15:00Z', updated_at: '2026-10-24T00:01:00Z', other_hold: false,
  evidence: { samples: 3, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: 500, ttft_samples: 3, cache_rate: 0, cache_samples: 2, fresh: 2, complete: true, latest: '' },
  ...overrides,
})
function makeWrapper(policyId?: number) {
  return mount(GroupQualityTargetsPanel, { props: { groups, visible: true, policyId }, global: {
    plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
    stubs: { BaseDialog: { props: ['show', 'title'], template: '<section v-if="show" data-dialog><h2>{{ title }}</h2><slot/><slot name="footer"/></section>' } },
  } })
}
beforeEach(() => { vi.resetAllMocks(); api.policies.mockResolvedValue([policy()]); api.states.mockResolvedValue([]); api.groupTargets.mockResolvedValue([summary()]); api.saveGroupQualityTarget.mockResolvedValue(policy()) })
afterEach(() => vi.useRealTimers())

describe('simple group quality targets', () => {
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

  it('uses 80/80/10 for Pro and other groups, and switches to the existing target without creating a duplicate', async () => {
    const w = makeWrapper(); await flushPromises(); await w.get('[data-target-create]').trigger('click')
    await w.get('[data-target-group]').setValue('5')
    expect((w.get('[data-target-cache]').element as HTMLInputElement).value).toBe('80')
    expect((w.get('[data-target-availability]').element as HTMLInputElement).value).toBe('80')
    expect((w.get('[data-target-ttft]').element as HTMLInputElement).value).toBe('10')
    await w.get('[data-target-group]').setValue('3')
    expect(w.get('[data-dialog]').text()).toContain('updates the existing policy')
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

  it('counts only healthy, complete, sufficiently sampled target evidence without another hold', async () => {
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
