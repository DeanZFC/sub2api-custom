import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import ChannelQualityOverview from '../ChannelQualityOverview.vue'
import resources from '@/i18n/locales/en/admin/resources'
const api = vi.hoisted(() => ({ overview: vi.fn() }))
vi.mock('@/api/admin/channelQuality', () => ({ channelQualityAPI: api }))
const account = (id: number) => ({ id, name: `Provider ${id}`, platform: 'openai', status: 'active', schedulable: true, active_plans: 1, passive_policies: 1, controls: [] })
const page = (ids: number[], next_after = 0) => ({ items: ids.map(account), next_after, generated_at: '2026-10-22T12:00:00Z' })
const mountView = () => mount(ChannelQualityOverview, { props: { groups: [], visible: true }, global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })] } })
beforeEach(() => { vi.resetAllMocks(); api.overview.mockResolvedValue(page([1])) })
afterEach(() => vi.useRealTimers())
describe('quality overview', () => {
  it('shows model-scoped active pass-rate evidence and opens its plan', async () => {
    api.overview.mockResolvedValue({ ...page([]), items: [{ ...account(1), controls: [{
      source: 'active', id: 7, metric: 'pass_rate', name: 'Candy', model: 'A', scope: 'model',
      phase: 'paused', reason: 'Recent capability failures', recovery: 'active_tests', blocking: true,
      pass_rate: {
        samples: 10, passed: 7, pass_rate: 70, window_size: 10, window_seconds: 604800,
        pause_below: 80, recover_at_least: 90, recover_consecutive: 2, consecutive_passes: 1,
        blocked: true, ready: true, stale: false, reason: 'recovery_pending',
        latest_verdict: 'pass', rule_version: 'v1', model: 'A',
      },
    }] }] })
    const wrapper = mountView(); await flushPromises()
    expect(wrapper.text()).toContain('Capability pass rate')
    expect(wrapper.get('[data-pass-rate-counts]').text()).toBe('7/10 passed · 70%')
    expect(wrapper.get('[data-pass-rate-recovery-requirement]').text()).toContain('90%')
    await wrapper.findAll('button').find(button => button.text() === 'Inspect and manage')!.trigger('click')
    expect(wrapper.emitted('navigate')).toEqual([['plans', 7, 1]])
    wrapper.unmount()
  })

  it('links to passive quality management and labels their actual traffic evidence', async () => {
    api.overview.mockResolvedValue({ ...page([]), items: [{ ...account(1), controls: [{ source: 'passive', id: 5, metric: 'group_quality_target', name: 'Group targets', model: 'A', scope: 'model', phase: 'paused', reason: 'group_target_trial', recovery: 'real_traffic_trial', blocking: true, evidence: { samples: 5, error_rate: 20, ttft: 1000, cache_rate: 0 } }] }] })
    const w = mountView(); await flushPromises()
    expect(w.text()).toContain('Group quality targets · actual traffic')
    expect(w.text()).toContain('Availability 80% · First-token P90 1 s · Actual cache 0%')
    expect(w.text()).toContain('bounded trial verified with fresh production traffic')
    await w.get('[data-overview-goal="passive"]').trigger('click')
    expect(w.emitted('navigate')).toEqual([['passive']])
    w.unmount()
  })
  it('does not discard appended accounts on the refresh timer', async () => {
    vi.useFakeTimers(); api.overview.mockResolvedValueOnce(page([1], 1)).mockResolvedValueOnce(page([2]))
    const w = mountView(); await flushPromises()
    await w.findAll('button').find(b => b.text() === resources.tests.passive.more)!.trigger('click'); await flushPromises()
    expect(w.findAll('[data-quality-account]')).toHaveLength(2)
    await vi.advanceTimersByTimeAsync(30000); await flushPromises(); expect(api.overview).toHaveBeenCalledTimes(2)
    w.unmount()
  })
  it('starts a new page when filters change before load more', async () => {
    api.overview.mockResolvedValueOnce(page([1], 1)).mockResolvedValueOnce(page([3]))
    const w = mountView(); await flushPromises(); await w.get('form input').setValue('Other')
    await w.findAll('button').find(b => b.text() === resources.tests.passive.more)!.trigger('click'); await flushPromises()
    expect(api.overview).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'Other', after: undefined }))
    expect(w.findAll('[data-quality-account]')).toHaveLength(1); expect(w.text()).toContain('Provider 3'); w.unmount()
  })
  it('shows independent holds and navigates to the responsible policy', async () => {
    const item = { ...account(1), controls: [{ source: 'passive', id: 5, name: 'Slow upstream', model: 'A', scope: 'model', phase: 'limited', reason: 'recovered', recovery: 'production_samples', blocking: false }, { source: 'account', id: 0, name: '', scope: 'account', phase: 'paused', reason: 'manual_stop', recovery: 'manual', blocking: true }] }
    api.overview.mockResolvedValue({ ...page([]), items: [item] })
    const w = mountView(); await flushPromises(); expect(w.text()).toContain('Scheduling manually disabled'); expect(w.text()).toContain('Limited concurrency')
    await w.findAll('button').find(b => b.text() === 'Inspect and manage')!.trigger('click')
    expect(w.emitted('navigate')).toEqual([['records', 5, 1]]); w.unmount()
  })
  it('shows cache-only reduction without claiming concurrency or availability failure', async () => {
    const evidence = { cache_rate: 5, protection_rate: null, total_samples: 40, reusable_samples: 0, cold_samples: 30, unknown_samples: 10, missing_telemetry_samples: 0 }
    api.overview.mockResolvedValue({ ...page([]), items: [{ ...account(1), controls: [{ source: 'passive', id: 5, metric: 'cache', name: 'Cache policy', model: 'A', scope: 'model', phase: 'cache_limited', reason: 'cache_minimum_hold', recovery: 'cache_reuse_samples', blocking: false, allocation_percent: 25, cache: { model: 'A', action: evidence, quick: evidence } }] }] })
    const w = mountView(); await flushPromises()
    expect(w.text()).toContain('New-session allocation reduced')
    expect(w.get('[data-overview-cache]').text()).toContain('New-session allocation 25%')
    expect(w.get('[data-overview-cache]').text()).toContain('Reusable-request protection rate: —')
    expect(w.text()).not.toContain('Concurrency and priority reduced')
    expect(w.text()).not.toContain('Scheduling restricted')
    w.unmount()
  })

  it('shows a recent account quality score and sample confidence', async () => {
    api.overview.mockResolvedValue({ ...page([]), items: [{ ...account(1), quality_score: { value: 82.4, samples: 16, models: 2, latest_at: '2026-10-22T12:00:00Z' } }] })
    const w = mountView(); await flushPromises()
    expect(w.get('[data-quality-score]').text()).toContain('Quality score')
    expect(w.get('[data-quality-score]').text()).toContain('82')
    expect(w.get('[data-quality-score]').text()).toContain('2 models · 16 samples')
    w.unmount()
  })

  it('labels missing account scores as unavailable instead of zero', async () => {
    const w = mountView(); await flushPromises()
    expect(w.get('[data-quality-score]').text()).toContain('—')
    expect(w.get('[data-quality-score]').text()).toContain('No valid samples')
    w.unmount()
  })

})
