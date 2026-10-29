import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import type { OpsAccountRequestStats, OpsRequestDetail } from '@/api/admin/ops'
import RecentRequestsCell from '@/components/account/RecentRequestsCell.vue'
import AccountsView from '../AccountsView.vue'

const {
  listAccounts, listRequestDetails, getAccountRequestStatsBatch,
  getPlatformCounts, getBatchTodayStats, getUpstreamBillingProbeSettings, getAllProxies, getAllGroups,
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listRequestDetails: vi.fn(),
  getAccountRequestStatsBatch: vi.fn(),
  getPlatformCounts: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
}))

vi.mock('@/api/admin/channelQuality', () => ({ channelQualityAPI: { scores: async () => ({}) } }))
vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      getPlatformCounts,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
    },
    ops: { listRequestDetails, getAccountRequestStatsBatch },
    proxies: { getAll: getAllProxies },
    groups: { getAll: getAllGroups },
  },
}))
vi.mock('@/stores/app', () => ({
  useAppStore: () => ({ showError: vi.fn(), showWarning: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }),
}))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ token: 'test-token', isSimpleMode: false }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key }),
}))

const account = {
  id: 42, name: 'recent-request-account', platform: 'openai', type: 'oauth',
  status: 'active', schedulable: true, concurrency: 2, priority: 1,
  group_ids: [], extra: {}, credentials: {},
}
const request: OpsRequestDetail = {
  kind: 'success', request_id: 'request-42', created_at: '2026-09-11T10:27:52+08:00',
  account_id: 42, request_type: 'stream', stream: true,
  input_tokens: 100, cache_read_tokens: 0, cache_creation_tokens: 0,
  first_token_ms: 1000, duration_ms: 2000,
}
const statistics: OpsAccountRequestStats = {
  window_start: '2026-09-11T09:30:00+08:00', window_end: '2026-09-11T10:30:00+08:00',
  cache_rate: 0.8, avg_first_token_ms: 3000, avg_duration_ms: 8000,
  cache_samples: 32, first_token_samples: 33, duration_samples: 35,
}
let wrapper: VueWrapper | undefined

function mountView() {
  wrapper = shallowMount(AccountsView, {
    global: {
      stubs: {
        AppLayout: { template: '<div><slot /></div>' },
        TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
        DataTable: {
          props: ['data'],
          template: '<div><div v-for="row in data" :key="row.id"><slot name="cell-recent_requests" :row="row" /></div></div>',
        },
      },
    },
  })
  return wrapper
}

describe('admin AccountsView recent request data sources', () => {
  beforeEach(() => {
    vi.useFakeTimers()
    vi.spyOn(document, 'hidden', 'get').mockReturnValue(false)
    localStorage.clear()
    listAccounts.mockReset().mockResolvedValue({ items: [account], total: 1, page: 1, page_size: 20, pages: 1 })
    listRequestDetails.mockReset().mockResolvedValue({ items: [request], total: 1, page: 1, page_size: 10 })
    getAccountRequestStatsBatch.mockReset().mockResolvedValue({ stats: { '42': statistics } })
    getPlatformCounts.mockReset().mockResolvedValue([])
    getBatchTodayStats.mockReset().mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockReset().mockResolvedValue({ enabled: false })
    getAllProxies.mockReset().mockResolvedValue([])
    getAllGroups.mockReset().mockResolvedValue([])
  })

  afterEach(() => {
    wrapper?.unmount()
    wrapper = undefined
    vi.restoreAllMocks()
    vi.useRealTimers()
  })

  it('loads the latest ten indicators separately from the complete hourly statistics batch', async () => {
    const view = mountView()
    await flushPromises()

    expect(listRequestDetails).toHaveBeenCalledWith({
      account_id: 42, time_range: '24h', kind: 'all', sort: 'created_at_desc', page: 1, page_size: 10,
    })
    expect(getAccountRequestStatsBatch).toHaveBeenCalledWith([42])
    const cell = view.getComponent(RecentRequestsCell)
    expect(cell.props('requests')).toEqual([request])
    expect(cell.props('statistics')).toEqual(statistics)
    expect(cell.props('statisticsLoadError')).toBe(false)
  })

  it('refreshes the two sources independently and preserves the previous hourly snapshot while loading', async () => {
    const view = mountView()
    await flushPromises()
    let resolveStatistics!: (value: { stats: Record<string, OpsAccountRequestStats> }) => void
    getAccountRequestStatsBatch.mockImplementationOnce(() => new Promise(resolve => { resolveStatistics = resolve }))
    const newerRequest = { ...request, request_id: 'newer-request' }
    listRequestDetails.mockResolvedValueOnce({ items: [newerRequest], total: 1, page: 1, page_size: 10 })

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(getAccountRequestStatsBatch).toHaveBeenCalledTimes(2)
    expect(view.getComponent(RecentRequestsCell).props('statistics')).toEqual(statistics)
    expect(view.getComponent(RecentRequestsCell).props('requests')).toEqual([newerRequest])
    resolveStatistics({ stats: { '42': { ...statistics, avg_duration_ms: 9000 } } })
    await flushPromises()

    const cell = view.getComponent(RecentRequestsCell)
    expect(cell.props('requests')).toEqual([newerRequest])
    expect(cell.props('statistics')).toEqual({ ...statistics, avg_duration_ms: 9000 })
    expect(cell.props('statisticsLoading')).toBe(false)
  })

  it('marks only statistics as failed when the hourly batch fails, then recovers on the next refresh', async () => {
    const view = mountView()
    await flushPromises()
    getAccountRequestStatsBatch.mockRejectedValueOnce(new Error('hourly statistics unavailable'))

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    let cell = view.getComponent(RecentRequestsCell)
    expect(cell.props('requests')).toEqual([request])
    expect(cell.props('loadError')).toBe(false)
    expect(cell.props('statisticsLoadError')).toBe(true)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    cell = view.getComponent(RecentRequestsCell)
    expect(cell.props('statisticsLoadError')).toBe(false)
    expect(cell.props('statistics')).toEqual(statistics)
  })

  it('publishes hourly statistics before slow details finish and avoids overlapping refreshes for the same accounts', async () => {
    let resolveDetails!: (value: { items: OpsRequestDetail[]; total: number; page: number; page_size: number }) => void
    let resolveStatistics!: (value: { stats: Record<string, OpsAccountRequestStats> }) => void
    listRequestDetails.mockImplementationOnce(() => new Promise(resolve => { resolveDetails = resolve }))
    getAccountRequestStatsBatch.mockImplementationOnce(() => new Promise(resolve => { resolveStatistics = resolve }))
    const view = mountView()
    await flushPromises()

    expect(listRequestDetails).toHaveBeenCalledTimes(1)
    expect(getAccountRequestStatsBatch).toHaveBeenCalledTimes(1)
    await vi.advanceTimersByTimeAsync(10_000)
    await flushPromises()
    expect(listRequestDetails).toHaveBeenCalledTimes(1)
    expect(getAccountRequestStatsBatch).toHaveBeenCalledTimes(1)

    resolveStatistics({ stats: { '42': statistics } })
    await flushPromises()
    let cell = view.getComponent(RecentRequestsCell)
    expect(cell.props('statistics')).toEqual(statistics)
    expect(cell.props('statisticsLoading')).toBe(false)
    expect(cell.props('requests')).toEqual([])
    expect(cell.props('loading')).toBe(true)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(listRequestDetails).toHaveBeenCalledTimes(1)
    expect(getAccountRequestStatsBatch).toHaveBeenCalledTimes(1)

    resolveDetails({ items: [request], total: 1, page: 1, page_size: 10 })
    await flushPromises()
    cell = view.getComponent(RecentRequestsCell)
    expect(cell.props('requests')).toEqual([request])
    expect(cell.props('statistics')).toEqual(statistics)
    expect(cell.props('loading')).toBe(false)

    await vi.advanceTimersByTimeAsync(5000)
    await flushPromises()
    expect(listRequestDetails).toHaveBeenCalledTimes(2)
    expect(getAccountRequestStatsBatch).toHaveBeenCalledTimes(2)
    expect(view.getComponent(RecentRequestsCell).props('statisticsLoadError')).toBe(false)
  })
})
