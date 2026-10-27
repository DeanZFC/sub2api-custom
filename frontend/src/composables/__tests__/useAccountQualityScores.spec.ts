import { defineComponent, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { useAccountQualityScores } from '../useAccountQualityScores'
import type { ChannelQualityScore } from '@/api/admin/channelQuality'
const api = vi.hoisted(() => ({ scores: vi.fn() }))
vi.mock('@/api/admin/channelQuality', () => ({ channelQualityAPI: api }))
const score = (value: number) => ({ value, status: 'ready' }) as ChannelQualityScore
beforeEach(() => vi.resetAllMocks())
describe('account list quality scores', () => {
  it('batches the current page and leaves a failed batch unknown', async () => {
    api.scores.mockResolvedValueOnce({ '1': score(85) }).mockRejectedValueOnce(new Error('offline'))
    const accounts = ref(Array.from({ length: 101 }, (_, i) => ({ id: i + 1 })))
    let state!: ReturnType<typeof useAccountQualityScores>
    const wrapper = mount(defineComponent({ setup() { state = useAccountQualityScores(accounts, ref(true)); return () => null } }))
    await flushPromises()
    expect(api.scores).toHaveBeenCalledTimes(2)
    expect(api.scores.mock.calls[0][0]).toHaveLength(100)
    expect(api.scores.mock.calls[1][0]).toEqual([101])
    expect(state.scores.value['1'].value).toBe(85)
    expect(state.scores.value['101']).toBeUndefined()
    wrapper.unmount()
  })
  it('ignores stale page responses and clears data when the column is hidden', async () => {
    let resolveFirst!: (value: Record<string, ChannelQualityScore>) => void
    api.scores.mockReturnValueOnce(new Promise(resolve => { resolveFirst = resolve })).mockResolvedValueOnce({ '2': score(90) })
    const accounts = ref([{ id: 1 }]), enabled = ref(true)
    let state!: ReturnType<typeof useAccountQualityScores>
    const wrapper = mount(defineComponent({ setup() { state = useAccountQualityScores(accounts, enabled); return () => null } }))
    accounts.value = [{ id: 2 }]; await flushPromises()
    resolveFirst({ '1': score(50) }); await flushPromises()
    expect(Object.keys(state.scores.value)).toEqual(['2'])
    enabled.value = false; await flushPromises()
    expect(state.scores.value).toEqual({})
    expect(api.scores).toHaveBeenCalledTimes(2)
    wrapper.unmount()
  })
})
