import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { toRaw } from 'vue'
import { createI18n } from 'vue-i18n'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import Dialog from '../AccountPerformanceDialog.vue'
import en from '@/i18n/locales/en'
import zh from '@/i18n/locales/zh'
import { emptyPerformanceConfig, type AccountPerformance } from '@/types/accountPerformance'
const api = vi.hoisted(() => ({ getPerformance: vi.fn(), getPerformanceEvents: vi.fn(), savePerformance: vi.fn(), releasePerformance: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: api } }))
let wrapper: VueWrapper | undefined
const metric: AccountPerformance = {
 monitoring_available: true, account_id: 7, config: emptyPerformanceConfig(), revision: 4, paused: false, pause_generation: 0,
 paused_at: null, trial_started_at: null, trial_requests: 0, metrics_since: null, reason: '',
 metrics: { window_start: '2026-11-01T23:00:00Z', window_end: '2026-11-02T00:00:00Z', success_rate: null, success_requests: 0, error_requests: 0, request_samples: 0, cache_rate: null, avg_first_token_ms: null, avg_duration_ms: null, cache_samples: 0, first_token_samples: 0, duration_samples: 0 }
}
function performanceSnapshot(phase: AccountPerformance['phase'] = 'disabled') {
 const state = structuredClone(metric)
 state.phase = phase
 if (phase !== 'disabled') {
  state.config.enabled = true
  state.config.success_rate.pause.min = 90
 }
 if (phase === 'paused' || phase === 'trial') {
  state.paused = true
  state.pause_generation = 2
  state.paused_at = '2026-11-02T00:00:00Z'
  state.reason = 'success_rate'
 }
 if (phase === 'trial') {
  state.trial_started_at = '2026-11-02T00:00:00Z'
  state.trial_ends_at = '2026-11-02T00:10:00Z'
  state.trial_limit = 9
 }
 return state
}
async function open(locale = 'en') {
 wrapper = mount(Dialog, { props: { accountId: 7, accountName: 'Test account' }, global: { plugins: [createI18n({ legacy: false, locale, messages: { en, zh } })], stubs: { BaseDialog: { template: '<section><slot/><slot name="footer"/></section>' } } } })
 await flushPromises(); return wrapper
}
beforeEach(() => { vi.resetAllMocks(); api.getPerformance.mockResolvedValue({ 7: performanceSnapshot() }); api.getPerformanceEvents.mockResolvedValue([]) })
afterEach(() => {
  wrapper?.unmount()
  vi.useRealTimers()
})
describe('Account metric rules', () => {
 it.each(['disabled', 'monitoring', 'paused', 'trial'] as const)('opens the %s phase in its appropriate view and unmounts the other view', async phase => {
  api.getPerformance.mockResolvedValue({ 7: performanceSnapshot(phase) })
  const w = await open()
  const status = w.get('[data-testid="performance-status-view"]')
  const rules = w.get('[data-testid="performance-rules-view"]')
  const statusSelected = phase !== 'disabled'
  expect(status.attributes('aria-pressed')).toBe(String(statusSelected))
  expect(rules.attributes('aria-pressed')).toBe(String(!statusSelected))
  expect(w.find('[data-testid="monitor-enabled"]').exists()).toBe(!statusSelected)
  expect(w.find('dl [data-metric]').exists()).toBe(statusSelected)
  await (statusSelected ? rules : status).trigger('click')
  expect(w.find('[data-testid="monitor-enabled"]').exists()).toBe(statusSelected)
  expect(w.find('dl [data-metric]').exists()).toBe(!statusSelected)
 })

 it('saves all metrics and recovery thresholds with exact revision', async () => {
  const w = await open(); await w.get('[data-testid="monitor-enabled"]').setValue(true)
  await w.get('[data-testid="performance-advanced"]').trigger('click')
  for (const [key,value] of Object.entries({ 'success_rate-pauseMin':90, 'success_rate-recoveryMin':98, 'cache_rate-pauseMin':20, 'ttft-pauseMax':3000, 'duration-pauseMax':10000, 'cooldown-minutes':8 })) await w.get(`[data-testid="${key}"]`).setValue(value)
  api.savePerformance.mockImplementation(async (accountId: number, config: AccountPerformance['config']) => {
   const saved = performanceSnapshot('monitoring')
   saved.config = structuredClone(toRaw(config))
   saved.revision = 5
   api.getPerformance.mockResolvedValue({ [accountId]: saved })
  })
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(api.savePerformance).toHaveBeenCalledWith(7, expect.objectContaining({
   enabled: true, auto_recover: true, cooldown_minutes: 8, min_samples: 20, recovery_samples: 3,
   success_rate: { pause: { min: 90, max: null }, recovery: { min: 98, max: null } },
   cache_rate: { pause: { min: 20, max: null }, recovery: { min: null, max: null } },
   ttft: { pause: { min: null, max: 3000 }, recovery: { min: null, max: null } },
   duration: { pause: { min: null, max: 10000 }, recovery: { min: null, max: null } }
  }), 4)
  expect(w.emitted('updated')).toHaveLength(1)
  expect(w.get('[data-testid="performance-status-view"]').attributes('aria-pressed')).toBe('true')
  expect(w.get('[data-testid="performance-phase"]').text()).toBe('Monitoring metrics')
  expect(w.find('[data-testid="monitor-enabled"]').exists()).toBe(false)
 })
 it.each([['success_rate-pauseMin',101],['cache_rate-pauseMin',-1],['min-samples',0],['recovery-samples',101],['cooldown-minutes',1.5]])('rejects invalid %s', async (field,value) => {
  const w=await open(); await w.get(`[data-testid="${field}"]`).setValue(value)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(api.savePerformance).not.toHaveBeenCalled(); expect(w.find('[role="alert"]').exists()).toBe(true)
 })
 it('rejects recovery looser than pause and enabling with no selected metric', async () => {
  const w=await open(); await w.get('[data-testid="monitor-enabled"]').setValue(true)
  await w.get('[data-testid="performance-save"]').trigger('click'); expect(api.savePerformance).not.toHaveBeenCalled()
  await w.get('[data-testid="performance-advanced"]').trigger('click')
  await w.get('[data-testid="success_rate-pauseMin"]').setValue(90); await w.get('[data-testid="success_rate-recoveryMin"]').setValue(80)
  await w.get('[data-testid="performance-save"]').trigger('click'); expect(api.savePerformance).not.toHaveBeenCalled()
 })
 it('preserves zero thresholds', async () => {
  const w=await open(); await w.get('[data-testid="cache_rate-pauseMin"]').setValue(0)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(api.savePerformance).toHaveBeenCalledWith(7,expect.objectContaining({cache_rate:{pause:{min:0,max:null},recovery:{min:null,max:null}}}),4)
 })
 it('uses the pause generation for release', async () => {
  api.getPerformance.mockResolvedValue({ 7: performanceSnapshot('paused') })
  const w = await open()
  await w.get('[data-testid="performance-release"]').trigger('click')
  expect(api.releasePerformance).not.toHaveBeenCalled()
  w.getComponent({ name: 'ConfirmDialog' }).vm.$emit('cancel')
  await flushPromises()
  expect(w.findComponent({ name: 'ConfirmDialog' }).exists()).toBe(false)
  await w.get('[data-testid="performance-release"]').trigger('click')
  w.getComponent({ name: 'ConfirmDialog' }).vm.$emit('confirm')
  await flushPromises()
  expect(api.releasePerformance).toHaveBeenCalledWith(7,4,2)
 })
 it('retains edits on conflict', async () => {
  api.savePerformance.mockRejectedValue({response:{status:409}})
  const w=await open(); await w.get('[data-testid="success_rate-pauseMin"]').setValue(90)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('Reload'); expect(w.get<HTMLInputElement>('[data-testid="success_rate-pauseMin"]').element.value).toBe('90'); expect(w.emitted('updated')).toBeUndefined()
  const latest = performanceSnapshot()
  latest.revision = 5
  latest.config.success_rate.pause.min = 75
  api.getPerformance.mockResolvedValue({ 7: latest })
  const discardReload = w.findAll('button').find(button => button.text().includes('Discard edits and reload'))
  expect(discardReload).toBeDefined()
  await discardReload!.trigger('click')
  await flushPromises()
  expect(w.get('[data-testid="performance-rules-view"]').attributes('aria-pressed')).toBe('true')
  expect(w.get<HTMLInputElement>('[data-testid="success_rate-pauseMin"]').element.value).toBe('75')
  await w.get('[data-testid="success_rate-pauseMin"]').setValue(80)
  await w.get('[data-testid="performance-save"]').trigger('click')
  expect(api.savePerformance).toHaveBeenLastCalledWith(7, expect.objectContaining({ success_rate: expect.objectContaining({ pause: { min: 80, max: null } }) }), 5)
 })
 it.each(['en','zh'])('shows unknown values, pause reason and compact threshold controls in %s',async locale=>{
  api.getPerformance.mockResolvedValue({ 7: performanceSnapshot('paused') })
  const w = await open(locale)
  expect(w.text()).toContain('—')
  expect(w.text()).toContain(locale === 'zh' ? '暂停原因：成功率' : 'Pause reason: Success rate')
  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  expect(w.findAll('[data-threshold]')).toHaveLength(4)
  expect(w.find('[data-testid="success_rate-recoveryMin"]').exists()).toBe(false)
  expect(w.get('[data-testid="performance-save"]').attributes('disabled')).toBeDefined()
 })
 it('retains existing custom and zero bounds when saving in compact mode', async () => {
  const configured = structuredClone(metric)
  configured.config.ttft = { pause: { min: 0, max: 20000 }, recovery: { min: 5, max: 15000 } }
  api.getPerformance.mockResolvedValue({ 7: configured })
  const w = await open()
  expect(w.get('[data-testid="ttft-pauseMin"]').element).toHaveProperty('value', '0')
  await w.get('[data-testid="performance-advanced"]').trigger('click')
  await w.get('[data-testid="min-samples"]').setValue(25)
  await w.get('[data-testid="performance-save"]').trigger('click')
  expect(api.savePerformance).toHaveBeenCalledWith(7, expect.objectContaining({ ttft: configured.config.ttft }), 4)
 })
 it('uses the saved trial target for progress and displays known event reasons', async () => {
  const trial = performanceSnapshot('trial')
  trial.config.ttft.pause.max = 20000
  trial.trial_requests = 6
  trial.trial_limit = 9
  trial.metrics.first_token_samples = 1
  api.getPerformance.mockResolvedValue({ 7: trial })
  api.getPerformanceEvents.mockResolvedValue([
    { id: 1, action: 'pause', created_at: trial.trial_ends_at, reason: 'trial_insufficient_samples' },
    { id: 2, action: 'pause', created_at: trial.trial_ends_at, evidence: { reason: 'obsolete_reason' } },
  ])
  const w = await open()
  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  await w.get('[data-testid="recovery-samples"]').setValue(4)
  await w.get('[data-testid="performance-status-view"]').trigger('click')
  expect(w.get('[data-testid="performance-phase"]').text()).toBe('Recovery trial')
  expect(w.text()).toContain('6/9 requests admitted')
  expect(w.get('[data-testid="trial-progress"]').text()).toContain('Waiting for enough valid samples')
  expect(w.text()).toContain('Recovery samples 1/3')
  expect(w.text()).toContain('Trial ends at')
  expect(w.text()).toContain('Trial ended with too few valid samples')
  expect(w.text()).not.toContain('obsolete_reason')
 })
 it('distinguishes waiting for traffic, missing per-metric samples and recovery evaluation', async () => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  const trial = performanceSnapshot('trial')
  trial.config.success_rate.pause.min = 90
  trial.config.ttft.pause.max = 20000
  const report = () => api.getPerformance.mockResolvedValue({ 7: structuredClone(trial) })
  report()
  const w = await open('zh')
  expect(w.get('[data-testid="trial-progress"]').text()).toContain('等待首个符合调度条件的请求')
  expect(w.text()).toContain('已放行 0/9 次')
  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  expect(w.text()).toContain('未绑定账号的新请求优先分给符合条件的试运行账号')
  await w.get('[data-testid="performance-status-view"]').trigger('click')

  trial.trial_requests = 4
  trial.metrics.request_samples = 3
  trial.metrics.first_token_samples = 2
  report()
  await vi.advanceTimersByTimeAsync(30000)
  await flushPromises()
  expect(w.get('[data-testid="trial-progress"]').text()).toContain('等待积累足够的有效样本')
  expect(w.text()).toContain('已放行 4/9 次')
  expect(w.text()).toContain('恢复样本 2/3')

  trial.metrics.first_token_samples = 3
  report()
  await vi.advanceTimersByTimeAsync(30000)
  await flushPromises()
  expect(w.get('[data-testid="trial-progress"]').text()).toContain('样本数已达到要求')
  expect(w.text()).toContain('恢复样本 3/3')
  expect(w.get('[data-testid="performance-phase"]').text()).toBe('恢复试运行中')
 })
 it('allows disabling a saved rule with monitoring offline', async () => {
  const unavailable = performanceSnapshot('monitoring')
  unavailable.monitoring_available = false
  unavailable.config.ttft.pause.max = 20000
  api.getPerformance.mockResolvedValue({ 7: unavailable })
  const w = await open()
  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  await w.get('[data-testid="monitor-enabled"]').setValue(false)
  await w.get('[data-testid="performance-save"]').trigger('click')
  expect(api.savePerformance).toHaveBeenCalledWith(7, expect.objectContaining({ enabled: false }), 4)
 })
 it.each(['background', 'manual'])('refreshes a live trial on %s refresh while keeping unfinished edits tied to their original revision', async mode => {
  vi.useFakeTimers({ toFake: ['setInterval', 'clearInterval'] })
  const trial = performanceSnapshot('trial')
  trial.config.ttft.pause.max = 20000
  trial.trial_requests = 1
  api.getPerformance.mockResolvedValue({ 7: trial })
  const w = await open()
  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  await w.get('[data-testid="ttft-pauseMax"]').setValue(19000)
  await w.get('[data-testid="performance-status-view"]').trigger('click')
  const newer = structuredClone(trial)
  newer.revision = 5
  newer.trial_requests = 4
  newer.config.ttft.pause.max = 18000
  api.getPerformance.mockResolvedValue({ 7: newer })
  if (mode === 'background') await vi.advanceTimersByTimeAsync(30000)
  else await w.get('[data-testid="performance-refresh"]').trigger('click')
  await flushPromises()
  expect(w.text()).toContain('4/9 requests admitted')
  expect(w.find('[data-testid="monitor-enabled"]').exists()).toBe(false)
  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  expect(w.get<HTMLInputElement>('[data-testid="ttft-pauseMax"]').element.value).toBe('19000')
  await w.get('[data-testid="performance-save"]').trigger('click')
  expect(api.savePerformance).toHaveBeenCalledWith(7, expect.objectContaining({ ttft: expect.objectContaining({ pause: { min: null, max: 19000 } }) }), 4)
 })
})

describe('Trial metric targets', () => {
 it.each(['en', 'zh'])('shows saved inclusive recovery bounds for configured metrics first in %s', async locale => {
  const trial = structuredClone(metric)
  trial.phase = 'trial'
  trial.config.enabled = true
  trial.config.cache_rate.pause = { min: 0, max: 100 }
  trial.config.cache_rate.recovery.max = 92.125
  trial.config.ttft.pause.max = 2500
  trial.config.duration.pause.min = 0
  trial.metrics.cache_samples = 2
  api.getPerformance.mockResolvedValue({ 7: trial })

  const w = await open(locale)
  const cards = () => w.findAll('dl [data-metric]')
  expect(cards().map(card => card.attributes('data-metric'))).toEqual(['cache_rate', 'ttft', 'duration', 'success_rate'])
  const chinese = locale === 'zh'
  expect(cards()[0].text()).toContain(chinese ? '恢复目标 0% 至 92.125%' : 'Recovery target 0% to 92.125%')
  expect(cards()[0].text()).toContain(chinese ? '恢复样本 2/3' : 'Recovery samples 2/3')
  expect(cards()[1].text()).toContain(chinese ? '≤ 2,500毫秒' : '≤ 2,500 ms')
  expect(cards()[2].text()).toContain(chinese ? '≥ 0毫秒' : '≥ 0 ms')
  expect(cards()[3].text()).not.toContain(chinese ? '恢复目标' : 'Recovery target')

  await w.get('[data-testid="performance-rules-view"]').trigger('click')
  await w.get('[data-testid="ttft-pauseMax"]').setValue(1200)
  await w.get('[data-testid="performance-status-view"]').trigger('click')
  expect(cards()[1].text()).toContain(chinese ? '≤ 2,500毫秒' : '≤ 2,500 ms')
 })
})
