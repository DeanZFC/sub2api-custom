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
})
