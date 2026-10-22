import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import QualityReplayPanel from '../QualityReplayPanel.vue'
import { defaultPassivePolicy } from '@/api/admin/passiveQuality'
import type { QualityReplayResult } from '@/api/admin/qualityOperations'
import type { AccountListItem } from '@/types'
import resources from '@/i18n/locales/en/admin/resources'
const api = vi.hoisted(() => ({ replay: vi.fn() }))
vi.mock('@/api/admin/qualityOperations', () => ({ qualityOperationsAPI: api }))
const policy = () => ({ ...defaultPassivePolicy(), name: 'Draft', model: 'test', group_ids: [8] })
const output: QualityReplayResult = { points: [], evaluations: 61, triggers: 2, pending: 10, warnings: ['recorded_traffic_only'] }
const mountPanel = () => mount(QualityReplayPanel, { props: { policy: policy(), accounts: [{ id: 62, name: 'A', group_ids: [8] }, { id: 63, name: 'B', group_ids: [9] }] as AccountListItem[] }, global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })] } })
beforeEach(() => { vi.clearAllMocks(); api.replay.mockResolvedValue(output) })
describe('draft policy replay', () => {
  it('replays an all-model account policy without inventing a model filter', async () => {
    const wrapper = mountPanel()
    await wrapper.setProps({ policy: { ...policy(), model: '', scope: 'account' } })
    expect(wrapper.text()).toContain('All models')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-run-replay]').trigger('click'); await flushPromises()
    expect(api.replay).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ model: '', scope: 'account' }) }), expect.any(AbortSignal))
    wrapper.unmount()
  })
  it('only submits selected in-scope accounts with an unsaved draft', async () => {
    const wrapper = mountPanel()
    expect(wrapper.findAll('input[type="checkbox"]')).toHaveLength(1)
    expect(wrapper.get('[data-run-replay]').attributes('disabled')).toBeDefined()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-run-replay]').trigger('click'); await flushPromises()
    expect(api.replay).toHaveBeenCalledWith(expect.objectContaining({ account_ids: [62], config: expect.objectContaining({ id: 0, name: 'Draft' }) }), expect.any(AbortSignal))
    expect(wrapper.text()).toContain('61 evaluations, 2 triggers')
    expect(wrapper.text()).toContain('Uses recorded traffic only')
    wrapper.unmount()
  })
  it('discards an old replay when the draft changes during evaluation', async () => {
    let resolve!: (result: QualityReplayResult) => void
    api.replay.mockImplementation(() => new Promise<QualityReplayResult>(r => { resolve = r }))
    const wrapper = mountPanel(); await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-run-replay]').trigger('click')
    const signal = api.replay.mock.calls[0][1] as AbortSignal
    await wrapper.setProps({ policy: { ...policy(), min_samples: 10 } }); expect(signal.aborted).toBe(true)
    resolve(output); await flushPromises(); expect(wrapper.find('[role="status"]').exists()).toBe(false)
    wrapper.unmount()
  })
})
