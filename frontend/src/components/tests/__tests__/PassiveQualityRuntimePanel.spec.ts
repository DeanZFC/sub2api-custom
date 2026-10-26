import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import PassiveQualityRuntimePanel from '../PassiveQualityRuntimePanel.vue'
import type { PassiveRuntime } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'

const api = vi.hoisted(() => ({ runtime: vi.fn() }))
vi.mock('@/api/admin/passiveQuality', () => ({ passiveQualityAPI: api }))
const runtime = (overrides: Partial<PassiveRuntime> = {}): PassiveRuntime => ({
  queue_depth: 12, queue_capacity: 1024, pending_records: 15, dropped_records: 3, pending_gap_scopes: 2,
  last_persisted_at: null, last_persist_error: '', last_sweep_started_at: null, last_sweep_succeeded_at: null,
  last_sweep_duration_ms: 150, last_sweep_error: '', sweep_skipped_backlog: 4,
  last_cleanup_succeeded_at: null, last_cleanup_error: '', collection_lag_seconds: 8.25, ...overrides,
})
const makeWrapper = (visible = true) => mount(PassiveQualityRuntimePanel, { props: { visible }, global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
} })

beforeEach(() => { vi.resetAllMocks(); vi.useFakeTimers(); api.runtime.mockResolvedValue(runtime()) })
afterEach(() => { vi.restoreAllMocks(); vi.useRealTimers() })

describe('passive quality runtime diagnostics', () => {
  it('shows queue health and explicit unknown timestamps together with backend errors', async () => {
    api.runtime.mockResolvedValue(runtime({ last_persist_error: 'persist unavailable', last_sweep_error: 'evaluation unavailable', last_cleanup_error: 'cleanup unavailable' }))
    const wrapper = makeWrapper(); await flushPromises()
    const text = wrapper.get('[data-quality-runtime]').text()
    for (const value of ['12 / 1024', '15', '3', '2', '8.3 s', '150 ms', '4', 'persist unavailable', 'evaluation unavailable', 'cleanup unavailable']) expect(text).toContain(value)
    expect(text).toContain('Last persisted: —')
    expect(text).toContain('Last successful evaluation: —')
    expect(text).toContain('Last successful cleanup: —')
    expect(text).not.toContain('Invalid Date')
    wrapper.unmount()
  })

  it('retries a diagnostic request failure without inventing healthy values', async () => {
    api.runtime.mockRejectedValueOnce(new Error('offline'))
    const wrapper = makeWrapper(); await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('12 / 1024')
    await wrapper.get('[role="alert"] button').trigger('click'); await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('12 / 1024')
    wrapper.unmount()
  })

  it('polls only for a visible panel in an active document and stops after unmount', async () => {
    let hidden = false
    vi.spyOn(document, 'hidden', 'get').mockImplementation(() => hidden)
    const wrapper = makeWrapper(false); await flushPromises()
    await vi.advanceTimersByTimeAsync(15000)
    expect(api.runtime).not.toHaveBeenCalled()
    await wrapper.setProps({ visible: true }); await flushPromises()
    expect(api.runtime).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.runtime).toHaveBeenCalledTimes(2)
    hidden = true
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.runtime).toHaveBeenCalledTimes(2)
    hidden = false
    await wrapper.setProps({ visible: false })
    await vi.advanceTimersByTimeAsync(15000); await flushPromises()
    expect(api.runtime).toHaveBeenCalledTimes(2)
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.runtime).toHaveBeenCalledTimes(2)
  })

  it('keeps one request in flight when a polling interval elapses', async () => {
    let resolve!: (value: PassiveRuntime) => void
    api.runtime.mockImplementationOnce(() => new Promise(done => { resolve = done }))
    const wrapper = makeWrapper(); await flushPromises()
    await vi.advanceTimersByTimeAsync(30000)
    expect(api.runtime).toHaveBeenCalledTimes(1)
    resolve(runtime()); await flushPromises()
    expect(wrapper.text()).toContain('12 / 1024')
    wrapper.unmount()
  })
})
