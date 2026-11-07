import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
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
 monitoring_available: true, account_id: 7, config: emptyPerformanceConfig(), revision: 4, paused: true, pause_generation: 2,
 paused_at: '2026-11-02T00:00:00Z', trial_started_at: null, trial_requests: 0, metrics_since: null, reason: 'success_rate',
 metrics: { window_start: '2026-11-01T23:00:00Z', window_end: '2026-11-02T00:00:00Z', success_rate: null, success_requests: 0, error_requests: 0, request_samples: 0, cache_rate: null, avg_first_token_ms: null, avg_duration_ms: null, cache_samples: 0, first_token_samples: 0, duration_samples: 0 }
}
async function open(locale = 'en') {
 wrapper = mount(Dialog, { props: { accountId: 7, accountName: 'Test account' }, global: { plugins: [createI18n({ legacy: false, locale, messages: { en, zh } })], stubs: { BaseDialog: { template: '<section><slot/><slot name="footer"/></section>' } } } })
 await flushPromises(); return wrapper
}
beforeEach(() => { vi.resetAllMocks(); api.getPerformance.mockResolvedValue({ 7: structuredClone(metric) }); api.getPerformanceEvents.mockResolvedValue([]) })
afterEach(() => wrapper?.unmount())
describe('Account metric rules', () => {
 it('saves all metrics and recovery thresholds with exact revision', async () => {
  const w = await open(); await w.get('[data-testid="monitor-enabled"]').setValue(true)
  for (const [key,value] of Object.entries({ 'success_rate-pauseMin':90, 'success_rate-recoveryMin':98, 'cache_rate-pauseMin':20, 'ttft-pauseMax':3000, 'duration-pauseMax':10000, 'cooldown-minutes':8 })) await w.get(`[data-testid="${key}"]`).setValue(value)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(api.savePerformance).toHaveBeenCalledWith(7, expect.objectContaining({
   enabled: true, auto_recover: true, cooldown_minutes: 8, min_samples: 20, recovery_samples: 3,
   success_rate: { pause: { min: 90, max: null }, recovery: { min: 98, max: null } },
   cache_rate: { pause: { min: 20, max: null }, recovery: { min: null, max: null } },
   ttft: { pause: { min: null, max: 3000 }, recovery: { min: null, max: null } },
   duration: { pause: { min: null, max: 10000 }, recovery: { min: null, max: null } }
  }), 4); expect(w.emitted('updated')).toHaveLength(1)
 })
 it.each([['success_rate-pauseMin',101],['cache_rate-pauseMin',-1],['min-samples',0],['recovery-samples',101],['cooldown-minutes',1.5]])('rejects invalid %s', async (field,value) => {
  const w=await open(); await w.get(`[data-testid="${field}"]`).setValue(value)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(api.savePerformance).not.toHaveBeenCalled(); expect(w.find('[role="alert"]').exists()).toBe(true)
 })
 it('rejects recovery looser than pause and enabling with no selected metric', async () => {
  const w=await open(); await w.get('[data-testid="monitor-enabled"]').setValue(true)
  await w.get('[data-testid="performance-save"]').trigger('click'); expect(api.savePerformance).not.toHaveBeenCalled()
  await w.get('[data-testid="success_rate-pauseMin"]').setValue(90); await w.get('[data-testid="success_rate-recoveryMin"]').setValue(80)
  await w.get('[data-testid="performance-save"]').trigger('click'); expect(api.savePerformance).not.toHaveBeenCalled()
 })
 it('preserves zero thresholds', async () => {
  const w=await open(); await w.get('[data-testid="cache_rate-pauseMin"]').setValue(0)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(api.savePerformance).toHaveBeenCalledWith(7,expect.objectContaining({cache_rate:{pause:{min:0,max:null},recovery:{min:null,max:null}}}),4)
 })
 it('uses the pause generation for release', async () => {
  const w=await open(); await w.get('[data-testid="performance-release"]').trigger('click'); await flushPromises(); expect(api.releasePerformance).toHaveBeenCalledWith(7,4,2)
 })
 it('retains edits on conflict', async () => {
  api.savePerformance.mockRejectedValue({response:{status:409}})
  const w=await open(); await w.get('[data-testid="success_rate-pauseMin"]').setValue(90)
  await w.get('[data-testid="performance-save"]').trigger('click'); await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('Reload'); expect(w.get<HTMLInputElement>('[data-testid="success_rate-pauseMin"]').element.value).toBe('90'); expect(w.emitted('updated')).toBeUndefined()
 })
 it.each(['en','zh'])('explains unknown samples and independent recovery in %s',async locale=>{
  const w=await open(locale);expect(w.text()).toContain('—');expect(w.findAll('[data-threshold]')).toHaveLength(4)
  expect(w.text()).toContain(locale==='zh'?'无流量不会自动恢复':'No traffic means no recovery')
  expect(w.text()).toContain(locale==='zh'?'手动禁用':'Manual disables')
 })
})
