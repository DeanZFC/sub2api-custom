import { effectScope, nextTick, ref } from 'vue'
import { flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { useQQGroupNames } from '../useQQGroupNames'
import type { QQGroupInfo } from '@/api/qqbot'

const api = vi.hoisted(() => ({ groupInfo: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
const scopes: ReturnType<typeof effectScope>[] = []
const info = (appID: string, groupID: string, patch: Partial<QQGroupInfo> = {}): QQGroupInfo => ({ app_id: appID, sandbox: false, group_id: groupID, name: 'Group ' + groupID, status: 'available', ...patch })

function setup(groups = ['first', 'second']) {
  const source = ref<{ appID: string; sandbox: boolean; revision: string; groups: string[] } | null>({ appID: 'app', sandbox: false, revision: 'v1', groups })
  const scope = effectScope(); scopes.push(scope)
  const state = scope.run(() => useQQGroupNames(() => source.value))!
  return { source, scope, ...state }
}

describe('QQ group names', () => {
  beforeEach(() => {
    api.groupInfo.mockReset()
    api.groupInfo.mockImplementation(async (appID: string, groupID: string) => info(appID, groupID))
  })
  afterEach(() => { scopes.splice(0).forEach(scope => scope.stop()); vi.useRealTimers() })

  it('keeps names separate from policies and cancels stale application responses', async () => {
    let finish!: (value: QQGroupInfo) => void
    api.groupInfo.mockImplementationOnce(() => new Promise<QQGroupInfo>(resolve => { finish = resolve }))
    const state = setup(['first'])
    const oldSignal = api.groupInfo.mock.calls[0]![2] as AbortSignal
    state.source.value = { appID: 'new-app', sandbox: false, revision: 'v2', groups: ['second'] }
    await nextTick(); await flushPromises()
    expect(oldSignal.aborted).toBe(true)
    expect(state.names.value).toEqual({ second: 'Group second' })
    finish(info('app', 'first', { name: 'Old bot name' }))
    await flushPromises()
    expect(state.names.value).toEqual({ second: 'Group second' })
    state.source.value = null
    await nextTick()
    expect(state.names.value).toEqual({})
  })

  it('rejects responses for a different group or sandbox', async () => {
    api.groupInfo.mockResolvedValueOnce(info('app', 'wrong'))
      .mockResolvedValueOnce(info('app', 'second', { sandbox: true }))
    const state = setup()
    await flushPromises()
    expect(state.names.value).toEqual({})
    expect(state.status.value).toBe('unavailable')
  })

  it('stops permission failures across groups and retries after credentials change', async () => {
    api.groupInfo.mockResolvedValueOnce(info('app', 'first', { name: '', status: 'permission_required' }))
    const state = setup()
    await flushPromises()
    expect(api.groupInfo).toHaveBeenCalledTimes(1)
    state.source.value!.groups.push('third')
    await nextTick(); await flushPromises()
    expect(api.groupInfo).toHaveBeenCalledTimes(1)
    state.source.value!.revision = 'new-credentials'
    await nextTick(); await flushPromises()
    expect(Object.keys(state.names.value)).toEqual(['first', 'second', 'third'])
    expect(state.status.value).toBe('')
  })

  it('resumes after a rate limit and cancels pending retries on unmount', async () => {
    vi.useFakeTimers()
    api.groupInfo.mockResolvedValueOnce(info('app', 'first', { name: '', status: 'rate_limited', retry_after_seconds: 2 }))
    const state = setup()
    await flushPromises()
    expect(state.status.value).toBe('rate_limited')
    expect(state.loading.value).toBe(false)
    await vi.advanceTimersByTimeAsync(1999)
    expect(api.groupInfo).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(1)
    expect(state.names.value).toEqual({ first: 'Group first', second: 'Group second' })
    expect(state.status.value).toBe('')
    api.groupInfo.mockResolvedValueOnce(info('app', 'first', { name: '', status: 'rate_limited' }))
    state.refresh(); await flushPromises()
    const beforeStop = api.groupInfo.mock.calls.length
    state.scope.stop()
    await vi.advanceTimersByTimeAsync(120000)
    expect(api.groupInfo).toHaveBeenCalledTimes(beforeStop)
  })

  it('reuses completed names when another discovered group appears', async () => {
    const state = setup(['first'])
    await flushPromises()
    state.source.value!.groups.push('second')
    await nextTick(); await flushPromises()
    expect(api.groupInfo).toHaveBeenCalledTimes(2)
    expect(state.names.value).toEqual({ first: 'Group first', second: 'Group second' })
  })
})
