import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { defineComponent, h, nextTick, ref } from 'vue'
import { flushPromises, mount } from '@vue/test-utils'
import { getMatrix, type MonitorMatrixResponse, type MonitorMatrixRow } from '@/api/channelMonitorV2'
import { useGroupChannelStatus } from '@/composables/useGroupChannelStatus'

vi.mock('@/api/channelMonitorV2', () => ({ getMatrix: vi.fn() }))

const matrixMock = vi.mocked(getMatrix)
const wrappers: Array<{ unmount: () => void }> = []
let now: number

function row(groupId: number, platform = 'openai'): MonitorMatrixRow {
  return {
    group_id: groupId,
    group_name: `Group ${groupId}`,
    platform,
    metrics: {
      success_requests: 0, error_requests: 0, request_count: 0, token_count: 0,
      rpm: 0, tpm: 0, error_rate: 0, cache_rate: 0.8,
      cache_rate_numerator: 0, cache_rate_denominator: 0,
      ttft: { sample_count: 0, p50_ms: 100, p95_ms: 300, avg_ms: 200 },
      duration: { sample_count: 0, p50_ms: 1000, p95_ms: 3000, avg_ms: 2000 },
    },
    health: { overall: 'healthy', error_rate: 'healthy', ttft: 'healthy', minimum_sample: 5 },
    buckets: [],
  }
}

function response(items: MonitorMatrixRow[] = [row(7)]): MonitorMatrixResponse {
  return {
    group_by: 'platform_group',
    coverage: {
      requested_start: '2026-09-21T00:00:00Z', requested_end: '2026-09-21T01:30:00Z',
      coverage_start: '2026-09-21T00:00:00Z', data_through: '2026-09-21T01:30:00Z',
      computed_at: '2026-09-21T01:30:00Z', aggregation_lag_seconds: 0,
      coverage_complete: true, bucket_seconds: 60,
    },
    items,
  }
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((resolveFn, rejectFn) => {
    resolve = resolveFn
    reject = rejectFn
  })
  return { promise, resolve, reject }
}

function setup(initial: { enabled?: boolean; active?: boolean; userId?: number | null; groupIds?: number[] } = {}) {
  const enabled = ref(initial.enabled ?? true)
  const active = ref(initial.active ?? false)
  const userId = ref<number | null>(initial.userId === undefined ? 42 : initial.userId)
  const groupIds = ref(initial.groupIds ?? [7, 9])
  let status!: ReturnType<typeof useGroupChannelStatus>
  const wrapper = mount(defineComponent({
    setup() {
      status = useGroupChannelStatus({ enabled, active, userId, groupIds })
      return () => h('div')
    },
  }))
  wrappers.push(wrapper)
  return { status, enabled, active, userId, groupIds, wrapper }
}

async function settle() {
  await nextTick()
  await flushPromises()
}

describe('useGroupChannelStatus', () => {
  beforeEach(() => {
    now = Date.UTC(2026, 8, 21, 1, 30)
    vi.spyOn(Date, 'now').mockImplementation(() => now)
    matrixMock.mockReset()
    matrixMock.mockResolvedValue(response())
  })

  afterEach(() => {
    for (const wrapper of wrappers.splice(0)) wrapper.unmount()
    vi.restoreAllMocks()
  })

  it('loads only when an enabled selector is active and a user and group scope exist', async () => {
    const state = setup({ enabled: false, userId: null, groupIds: [] })
    await state.status.ensureLoaded()
    await settle()
    expect(matrixMock).not.toHaveBeenCalled()

    state.active.value = true
    await settle()
    state.enabled.value = true
    await settle()
    state.userId.value = 42
    await settle()
    expect(matrixMock).not.toHaveBeenCalled()

    state.active.value = false
    state.groupIds.value = [7]
    await settle()
    await state.status.ensureLoaded()
    expect(matrixMock).not.toHaveBeenCalled()

    state.active.value = true
    await settle()
    expect(matrixMock).toHaveBeenCalledTimes(1)
    expect(matrixMock).toHaveBeenCalledWith(
      { range: '90m', platforms: [], groupIds: [7], models: [] },
      'platform_group', false, expect.any(AbortSignal),
    )
  })

  it('deduplicates in-flight loads and normalizes reordered group scopes', async () => {
    const pending = deferred<MonitorMatrixResponse>()
    matrixMock.mockReturnValueOnce(pending.promise)
    const state = setup({ active: true, groupIds: [9, 7, 9] })
    await nextTick()
    const load = state.status.ensureLoaded()
    void state.status.ensureLoaded()
    expect(state.status.loading.value).toBe(true)
    expect(matrixMock).toHaveBeenCalledTimes(1)
    expect(matrixMock.mock.calls[0][0].groupIds).toEqual([7, 9])

    state.groupIds.value = [7, 9]
    await nextTick()
    expect(matrixMock.mock.calls[0][3]?.aborted).toBe(false)
    pending.resolve(response())
    await load
    await settle()
    expect(matrixMock).toHaveBeenCalledTimes(1)
    expect(state.status.loading.value).toBe(false)
    expect(state.status.coverage.value).toEqual(response().coverage)
  })

  it('uses a 60 second cache, does not poll, and clears stale bars before refresh', async () => {
    const interval = vi.spyOn(globalThis, 'setInterval')
    const state = setup({ active: true })
    await settle()
    expect(state.status.rowsForGroup(7, 'openai')).toHaveLength(1)

    state.active.value = false
    await nextTick()
    now += 59_000
    state.active.value = true
    await settle()
    expect(matrixMock).toHaveBeenCalledTimes(1)

    now += 2_000
    await settle()
    expect(matrixMock).toHaveBeenCalledTimes(1)
    expect(interval).not.toHaveBeenCalled()

    const refresh = deferred<MonitorMatrixResponse>()
    matrixMock.mockReturnValueOnce(refresh.promise)
    const load = state.status.ensureLoaded()
    expect(matrixMock).toHaveBeenCalledTimes(2)
    expect(state.status.coverage.value).toBeNull()
    expect(state.status.rowsForGroup(7, 'openai')).toEqual([])
    expect(state.status.loading.value).toBe(true)
    refresh.resolve(response([row(9)]))
    await load
    expect(state.status.rowsForGroup(9, 'openai')).toHaveLength(1)
  })

  it('matches both group and platform while composite groups include their platforms', async () => {
    matrixMock.mockResolvedValueOnce(response([row(7), row(7, 'anthropic'), row(9), { ...row(7), group_id: undefined }]))
    const state = setup({ active: true })
    await settle()
    expect(state.status.rowsForGroup(7, 'openai').map(value => value.platform)).toEqual(['openai'])
    expect(state.status.rowsForGroup(7, 'anthropic').map(value => value.platform)).toEqual(['anthropic'])
    expect(state.status.rowsForGroup(7, 'composite').map(value => value.platform)).toEqual(['openai', 'anthropic'])
    expect(state.status.rowsForGroup(9, 'composite')).toHaveLength(1)
    expect(state.status.rowsForGroup(8, 'openai')).toEqual([])
  })

  it('aborts superseded group loads and ignores late responses from the old scope', async () => {
    const old = deferred<MonitorMatrixResponse>()
    const current = deferred<MonitorMatrixResponse>()
    matrixMock.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const state = setup({ active: true, groupIds: [7] })
    const oldSignal = matrixMock.mock.calls[0][3]
    state.groupIds.value = [9]
    expect(oldSignal?.aborted).toBe(true)
    expect(state.status.coverage.value).toBeNull()
    await nextTick()
    expect(matrixMock).toHaveBeenCalledTimes(2)
    current.resolve(response([row(9)]))
    await settle()
    old.resolve(response([row(7)]))
    await settle()
    expect(state.status.rowsForGroup(7, 'openai')).toEqual([])
    expect(state.status.rowsForGroup(9, 'openai')).toHaveLength(1)
    expect(state.status.loading.value).toBe(false)
    expect(state.status.unavailable.value).toBe(false)
  })

  it('invalidates another user cache immediately and rejects late failed requests', async () => {
    const old = deferred<MonitorMatrixResponse>()
    const current = deferred<MonitorMatrixResponse>()
    matrixMock.mockReturnValueOnce(old.promise).mockReturnValueOnce(current.promise)
    const state = setup({ active: true })
    state.userId.value = 43
    expect(matrixMock.mock.calls[0][3]?.aborted).toBe(true)
    await nextTick()
    current.resolve(response([row(9)]))
    await settle()
    old.reject(new Error('old user request failed late'))
    await settle()
    expect(state.status.rowsForGroup(9, 'openai')).toHaveLength(1)
    expect(state.status.unavailable.value).toBe(false)

    state.userId.value = null
    expect(state.status.coverage.value).toBeNull()
    expect(state.status.rowsForGroup(9, 'openai')).toEqual([])
    await settle()
    expect(matrixMock).toHaveBeenCalledTimes(2)
  })

  it.each(['disabled', 'empty groups'] as const)('clears cached results and cancels in-flight loads when %s', async (reason) => {
    const state = setup({ active: true })
    await settle()
    expect(state.status.rowsForGroup(7, 'openai')).toHaveLength(1)
    now += 60_001
    const pending = deferred<MonitorMatrixResponse>()
    matrixMock.mockReturnValueOnce(pending.promise)
    const load = state.status.ensureLoaded()
    const signal = matrixMock.mock.calls[1][3]
    if (reason === 'disabled') state.enabled.value = false
    else state.groupIds.value = []
    expect(signal?.aborted).toBe(true)
    expect(state.status.coverage.value).toBeNull()
    expect(state.status.loading.value).toBe(false)
    pending.resolve(response())
    await load
    await settle()
    expect(state.status.rowsForGroup(7, 'openai')).toEqual([])
    expect(matrixMock).toHaveBeenCalledTimes(2)
  })

  it('degrades failed loads without rejection and backs off until the cache expires', async () => {
    matrixMock.mockRejectedValueOnce(new Error('monitor disabled'))
    const state = setup({ active: true })
    await settle()
    expect(state.status.unavailable.value).toBe(true)
    expect(state.status.loading.value).toBe(false)
    expect(state.status.coverage.value).toBeNull()
    await expect(state.status.ensureLoaded()).resolves.toBeUndefined()
    expect(matrixMock).toHaveBeenCalledTimes(1)

    now += 60_001
    await state.status.ensureLoaded()
    expect(matrixMock).toHaveBeenCalledTimes(2)
    expect(state.status.unavailable.value).toBe(false)
    expect(state.status.rowsForGroup(7, 'openai')).toHaveLength(1)
  })

  it('cancels a request on destruction and never writes its response or loads again', async () => {
    const pending = deferred<MonitorMatrixResponse>()
    matrixMock.mockReturnValueOnce(pending.promise)
    const state = setup({ active: true })
    const signal = matrixMock.mock.calls[0][3]
    state.wrapper.unmount()
    expect(signal?.aborted).toBe(true)
    pending.resolve(response())
    await settle()
    await state.status.ensureLoaded()
    expect(state.status.coverage.value).toBeNull()
    expect(state.status.loading.value).toBe(false)
    expect(matrixMock).toHaveBeenCalledTimes(1)
  })
})
