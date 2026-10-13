import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'

import AccountsView from '../AccountsView.vue'

const {
  listAccounts,
  listWithEtag,
  getPlatformCounts,
  batchRefresh,
  batchSyncUpstreamModels,
  getBatchTodayStats,
  getUpstreamBillingProbeSettings,
  getAllProxies,
  getAllGroups,
  showError
} = vi.hoisted(() => ({
  listAccounts: vi.fn(),
  listWithEtag: vi.fn(),
  getPlatformCounts: vi.fn(),
  batchRefresh: vi.fn(),
  batchSyncUpstreamModels: vi.fn(),
  getBatchTodayStats: vi.fn(),
  getUpstreamBillingProbeSettings: vi.fn(),
  getAllProxies: vi.fn(),
  getAllGroups: vi.fn(),
  showError: vi.fn()
}))

vi.mock('@/api/admin', () => ({
  adminAPI: {
    accounts: {
      list: listAccounts,
      listWithEtag,
      getPlatformCounts,
      getBatchTodayStats,
      getUpstreamBillingProbeSettings,
      batchDelete: vi.fn(),
      batchClearError: vi.fn(),
      batchRefresh,
      batchSyncUpstreamModels,
      bulkUpdate: vi.fn()
    },
    proxies: {
      getAll: getAllProxies
    },
    groups: {
      getAll: getAllGroups
    }
  }
}))

vi.mock('@/stores/app', () => ({
  useAppStore: () => ({
    showError,
    showSuccess: vi.fn(),
    showInfo: vi.fn()
  })
}))

vi.mock('@/stores/auth', () => ({
  useAuthStore: () => ({
    token: 'test-token'
  })
}))

vi.mock('vue-i18n', async () => {
  const actual = await vi.importActual<typeof import('vue-i18n')>('vue-i18n')
  return {
    ...actual,
    useI18n: () => ({
      t: (key: string) => key
    })
  }
})

const makeAccounts = (count: number) => Array.from({ length: count }, (_, index) => ({
  id: index + 1,
  name: `account-${index + 1}`,
  platform: 'grok',
  type: 'oauth',
  status: 'active',
  schedulable: true,
  created_at: '2026-07-23T00:00:00Z',
  updated_at: '2026-07-23T00:00:00Z'
}))

const AccountBulkActionsBarStub = {
  props: ['selectedIds', 'totalResults', 'selectingAll', 'allResultsSelected'],
  emits: ['select-all-results', 'select-page', 'clear', 'refresh-token', 'sync-upstream-models'],
  template: `
    <div>
      <span data-test="selected-count">{{ selectedIds.length }}</span>
      <span data-test="total-results">{{ totalResults }}</span>
      <span data-test="all-results-selected">{{ String(allResultsSelected) }}</span>
      <button data-test="select-page" @click="$emit('select-page')">select page</button>
      <button data-test="select-all-results" @click="$emit('select-all-results')">select all</button>
      <button data-test="clear" @click="$emit('clear')">clear</button>
      <button data-test="sync-upstream-models" @click="$emit('sync-upstream-models')">sync models</button>
      <button data-test="refresh-token" @click="$emit('refresh-token')">refresh token</button>
    </div>
  `
}

const AccountTableFiltersStub = {
  props: { filters: Object, hidePlatform: Boolean },
  emits: ['change', 'update:filters'],
  template: '<button data-test="change-filter" @click="$emit(\'change\')">change filter</button>'
}

const mountView = () => mount(AccountsView, {
  global: {
    stubs: {
      AppLayout: { template: '<div><slot /></div>' },
      TablePageLayout: {
        template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>'
      },
      DataTable: {
        props: ['data'],
        template: '<div data-test="data-table"><div v-for="row in data" :key="row.id"><slot name="cell-select" :row="row" /></div></div>'
      },
      Pagination: { emits: ['update:page'], template: '<button data-test="next-page" @click="$emit(\'update:page\', 2)">next page</button>' },
      ConfirmDialog: true,
      AccountTableActions: { template: '<div><slot name="beforeCreate" /><slot name="after" /></div>' },
      AccountTableFilters: AccountTableFiltersStub,
      AccountBulkActionsBar: AccountBulkActionsBarStub,
      AccountActionMenu: true,
      ImportDataModal: true,
      ReAuthAccountModal: true,
      AccountTestModal: true,
      AccountStatsModal: true,
      ScheduledTestsPanel: true,
      SyncFromCrsModal: true,
      TempUnschedStatusModal: true,
      ErrorPassthroughRulesModal: true,
      TLSFingerprintProfilesModal: true,
      CreateAccountModal: true,
      EditAccountModal: true,
      BulkEditAccountModal: true,
      PlatformTypeBadge: true,
      AccountCapacityCell: true,
      AccountStatusIndicator: true,
      AccountTodayStatsCell: true,
      AccountGroupsCell: true,
      AccountUsageCell: true,
      Icon: true
    }
  }
})

describe('admin AccountsView select all filtered results', () => {
  beforeEach(() => {
    localStorage.clear()
    listAccounts.mockReset()
    getPlatformCounts.mockReset().mockResolvedValue([])
    listWithEtag.mockReset()
    batchRefresh.mockReset()
    batchSyncUpstreamModels.mockReset()
    getBatchTodayStats.mockReset()
    getUpstreamBillingProbeSettings.mockReset()
    getAllProxies.mockReset()
    getAllGroups.mockReset()
    showError.mockReset()

    listWithEtag.mockResolvedValue({
      notModified: true,
      etag: null,
      data: null
    })
    getBatchTodayStats.mockResolvedValue({ stats: {} })
    getUpstreamBillingProbeSettings.mockResolvedValue({ enabled: true, interval_minutes: 30 })
    getAllProxies.mockResolvedValue([])
    getAllGroups.mockResolvedValue([])
  })

  afterEach(() => {
    vi.restoreAllMocks()
  })

  it.each([
    { name: 'keeps only failed accounts selected', result: { total: 3, success: 2, failed: 1, errors: [{ account_id: 2, error: 'no refresh token available' }] }, expectedIds: [2] },
    { name: 'clears the selection after every account succeeds', result: { total: 3, success: 3, failed: 0 }, expectedIds: [] },
    { name: 'keeps the original selection when failure details are missing', result: { total: 3, success: 2, failed: 1 }, expectedIds: [1, 2, 3] },
  ])('$name after a batch token refresh and table reload', async ({ result, expectedIds }) => {
    listAccounts.mockResolvedValue({ items: makeAccounts(3), total: 3, page: 1, page_size: 20, pages: 1 })
    batchRefresh.mockResolvedValue(result)
    vi.spyOn(window, 'confirm').mockReturnValue(true)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="select-page"]').trigger('click')
    await wrapper.get('[data-test="refresh-token"]').trigger('click')
    await flushPromises()

    expect(batchRefresh).toHaveBeenCalledWith([1, 2, 3])
    expect(listAccounts).toHaveBeenCalledTimes(2)
    expect(wrapper.getComponent(AccountBulkActionsBarStub).props('selectedIds')).toEqual(expectedIds)
    expect(wrapper.findAll<HTMLInputElement>('[data-test="data-table"] input').map(input => input.element.checked))
      .toEqual([1, 2, 3].map(id => expectedIds.includes(id)))
    if (result.failed > 0) {
      expect(showError).toHaveBeenCalledWith('admin.accounts.bulkActions.partialSuccess')
      await wrapper.get('[data-test="refresh-token"]').trigger('click')
      await flushPromises()
      expect(batchRefresh).toHaveBeenLastCalledWith(expectedIds)
    }
    wrapper.unmount()
  })

  it.each([
    { result: { total: 3, success: 2, failed: 1, success_ids: [1, 3], failed_ids: [2], errors: [{ account_id: 2, error: 'upstream failed' }] }, expectedIds: [2] },
    { result: { total: 3, success: 3, failed: 0, success_ids: [1, 2, 3], failed_ids: [], errors: [] }, expectedIds: [] },
  ])('keeps only failed accounts for retry after model sync ($result.failed failed)', async ({ result, expectedIds }) => {
    listAccounts.mockResolvedValue({ items: makeAccounts(3), total: 3, page: 1, page_size: 20, pages: 1 })
    batchSyncUpstreamModels.mockResolvedValue(result)
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="select-page"]').trigger('click')
    await wrapper.get('[data-test="sync-upstream-models"]').trigger('click')
    await flushPromises()

    expect(batchSyncUpstreamModels).toHaveBeenCalledWith([1, 2, 3])
    expect(wrapper.getComponent(AccountBulkActionsBarStub).props('selectedIds')).toEqual(expectedIds)
    if (result.failed > 0) {
      expect(wrapper.get('[data-test="bulk-model-sync-details"]').text()).toContain('#2: upstream failed')
      await wrapper.get('[data-test="sync-upstream-models"]').trigger('click')
      await flushPromises()
      expect(batchSyncUpstreamModels).toHaveBeenLastCalledWith([2])
    }
    wrapper.unmount()
  })

  it('keeps the selection when the bulk model sync request fails', async () => {
    listAccounts.mockResolvedValue({ items: makeAccounts(3), total: 3, page: 1, page_size: 20, pages: 1 })
    batchSyncUpstreamModels.mockRejectedValue(new Error('network error'))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="select-page"]').trigger('click')
    await wrapper.get('[data-test="sync-upstream-models"]').trigger('click')
    await flushPromises()
    expect(wrapper.getComponent(AccountBulkActionsBarStub).props('selectedIds')).toEqual([1, 2, 3])
    expect(showError).toHaveBeenCalled()
    wrapper.unmount()
  })

  it('ignores repeated sync clicks and preserves a new selection made while syncing', async () => {
    listAccounts.mockResolvedValue({ items: makeAccounts(3), total: 3, page: 1, page_size: 20, pages: 1 })
    let finishSync!: (result: unknown) => void
    batchSyncUpstreamModels.mockReturnValue(new Promise(resolve => { finishSync = resolve }))
    const wrapper = mountView()
    await flushPromises()
    await wrapper.get('[data-test="select-page"]').trigger('click')
    await wrapper.get('[data-test="sync-upstream-models"]').trigger('click')
    await wrapper.get('[data-test="sync-upstream-models"]').trigger('click')
    expect(batchSyncUpstreamModels).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-test="clear"]').trigger('click')
    await wrapper.findAll('[data-test="data-table"] input')[0]!.setValue(true)
    finishSync({ total: 3, success: 3, failed: 0, success_ids: [1, 2, 3], failed_ids: [], errors: [] })
    await flushPromises()
    expect(wrapper.getComponent(AccountBulkActionsBarStub).props('selectedIds')).toEqual([1])
    wrapper.unmount()
  })

  it('loads global platform tabs and switches server filters while clearing selection and pagination', async () => {
    const counts = [{ platform: 'grok', count: 45 }, { platform: 'openai', count: 1001 }]
    getPlatformCounts.mockResolvedValue(counts)
    listAccounts.mockResolvedValue({ items: makeAccounts(20), total: 1046, page: 1, page_size: 20, pages: 53 })
    const wrapper = mountView()
    await flushPromises()
    expect(getPlatformCounts).toHaveBeenCalledWith({ signal: expect.any(AbortSignal) })
    expect(wrapper.get('[role="tab"][data-platform="openai"]').text()).toContain('1001')
    expect(wrapper.get('[role="tab"][data-platform=""]').text()).toContain('1046')
    expect(wrapper.find('[role="tab"][data-platform="anthropic"]').exists()).toBe(false)
    expect(wrapper.getComponent(AccountTableFiltersStub).props('hidePlatform')).toBe(true)

    await wrapper.get('[data-test="next-page"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="select-page"]').trigger('click')
    wrapper.getComponent(AccountTableFiltersStub).vm.$emit('update:filters', { search: 'team', status: 'active', group: '7' })
    await wrapper.get('[role="tab"][data-platform="openai"]').trigger('click')
    await flushPromises()
    expect(listAccounts).toHaveBeenLastCalledWith(1, 20, expect.objectContaining({ platform: 'openai', search: 'team', status: 'active', group: '7' }), expect.any(Object))
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('0')
    expect(wrapper.get('[role="tab"][data-platform="openai"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[role="tab"][data-platform="grok"]').text()).toContain('45')
    expect(getPlatformCounts).toHaveBeenCalledTimes(1)

    await wrapper.get('[role="tab"][data-platform=""]').trigger('click')
    await flushPromises()
    expect(listAccounts).toHaveBeenLastCalledWith(1, 20, expect.objectContaining({ platform: '', search: 'team' }), expect.any(Object))
    wrapper.unmount()
  })

  it('refreshes platform counts after account creation and supports retry after an initial failure', async () => {
    listAccounts.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20, pages: 0 })
    getPlatformCounts.mockRejectedValueOnce(new Error('temporary failure'))
      .mockResolvedValueOnce([{ platform: 'openai', count: 1 }])
      .mockResolvedValueOnce([{ platform: 'openai', count: 1 }, { platform: 'deepseek', count: 1 }])
    const wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[role="status"]').text()).toContain('admin.accounts.platformTabs.loadFailed')
    await wrapper.get('[role="status"] button').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="status"]').exists()).toBe(false)
    expect(wrapper.get('[role="tab"][data-platform="openai"]').text()).toContain('1')
    wrapper.findComponent({ name: 'CreateAccountModal' }).vm.$emit('created')
    await flushPromises()
    expect(wrapper.get('[role="tab"][data-platform="deepseek"]').text()).toContain('1')
    expect(wrapper.get('[role="tab"][data-platform=""]').text()).toContain('2')
    wrapper.unmount()
  })

  it('selects all matching IDs in one commit and clears the selection when filters change', async () => {
    const allAccounts = makeAccounts(45)
    listAccounts.mockImplementation(async (_page: number, pageSize: number) => {
      if (pageSize === 1000) {
        return {
          items: allAccounts,
          total: 45,
          page: 1,
          page_size: 1000,
          pages: 1
        }
      }
      return {
        items: allAccounts.slice(0, 20),
        total: 45,
        page: 1,
        page_size: 20,
        pages: 3
      }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('45')
    expect(wrapper.get('[data-test="total-results"]').text()).toBe('45')
    expect(wrapper.get('[data-test="all-results-selected"]').text()).toBe('true')
    expect(listAccounts).toHaveBeenCalledWith(1, 1000, expect.objectContaining({
      lite: '1',
      include_scheduler_score: '0'
    }))

    await wrapper.get('[data-test="change-filter"]').trigger('click')

    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('0')
    expect(wrapper.get('[data-test="all-results-selected"]').text()).toBe('false')
  })

  it('keeps the original page selection when loading all results fails', async () => {
    const currentPage = makeAccounts(20)
    listAccounts.mockImplementation(async (_page: number, pageSize: number) => {
      if (pageSize === 1000) {
        throw new Error('load all failed')
      }
      return {
        items: currentPage,
        total: 45,
        page: 1,
        page_size: 20,
        pages: 3
      }
    })

    const wrapper = mountView()
    await flushPromises()

    await wrapper.get('[data-test="select-page"]').trigger('click')
    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('20')

    await wrapper.get('[data-test="select-all-results"]').trigger('click')
    await flushPromises()

    expect(wrapper.get('[data-test="selected-count"]').text()).toBe('20')
    expect(wrapper.get('[data-test="all-results-selected"]').text()).toBe('false')
    expect(showError).toHaveBeenCalledWith('admin.accounts.bulkActions.selectAllFailed')
  })
})
