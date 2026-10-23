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
  it('replays and displays each actual model independently at the same account and time', async () => {
    const at = new Date().toISOString()
    const evidence = { samples: 30, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: 10000, ttft_samples: 30, cache_rate: 0, cache_samples: 30, fresh: 5, complete: true, latest: at }
    api.replay.mockResolvedValue({ ...output, points: ['model-a', 'model-b'].map(model => ({ at, model, account_id: 62, verdict: 'fail', reason: 'cache_reuse_below_threshold', action: 'would_cache_reduced', hits: 3, evidence })) })
    const wrapper = mountPanel()
    await wrapper.setProps({ policy: { ...policy(), model: '', scope: 'model' } })
    expect(wrapper.text()).toContain('All models evaluated independently')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-run-replay]').trigger('click'); await flushPromises()
    expect(api.replay).toHaveBeenCalledWith(expect.objectContaining({ config: expect.objectContaining({ model: '', scope: 'model' }) }), expect.any(AbortSignal))
    const rows = wrapper.findAll('tbody tr')
    expect(rows).toHaveLength(2)
    expect(rows[0].text()).toContain('model-a')
    expect(rows[1].text()).toContain('model-b')
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
  it('labels independent simultaneous actions and explains unavailable historical reuse evidence', async () => {
    api.replay.mockResolvedValue({ ...output, warnings: ['cache_reuse_history_unavailable', 'cache_pool_guard_not_simulated'], points: [{ at: new Date().toISOString(), account_id: 62, verdict: 'fail', reason: 'cache_reuse_below_threshold', action: 'would_ttft_limited,would_cache_reduced', hits: 3, evidence: { samples: 30, failures: 0, consecutive_failures: 0, error_rate: 0, ttft: 10000, ttft_samples: 30, cache_rate: 0, cache_samples: 30, fresh: 5, complete: true, latest: '' } }] })
    const wrapper = mountPanel()
    expect(wrapper.get('[data-cache-replay-limit]').text()).toContain('requires reuse evidence recorded at the time')
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('[data-run-replay]').trigger('click'); await flushPromises()
    expect(wrapper.get('tbody tr').text()).toContain('test')
    expect(wrapper.get('tbody tr').text()).not.toContain('All models aggregated')
    expect(wrapper.text()).toContain('Latency limited independently · New-session allocation reduced')
    expect(wrapper.text()).toContain('Old history lacks pre-response reuse evidence')
    expect(wrapper.text()).toContain('the live cascade guard may prevent it')
    wrapper.unmount()
  })

})
