import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import VersionBadge from '../VersionBadge.vue'
import { getSourceUpdateStatus, performUpdate } from '@/api/admin/system'

vi.mock('@/api/admin/system', () => ({
  performUpdate: vi.fn(),
  getSourceUpdateStatus: vi.fn(),
  restartService: vi.fn(),
  getRollbackVersions: vi.fn(),
  rollback: vi.fn()
}))
vi.mock('@/stores', () => ({
  useAuthStore: () => ({ isAdmin: true }),
  useAppStore: () => store
}))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
vi.mock('@/composables/useClipboard', () => ({
  useClipboard: () => ({ copied: false, copyToClipboard: vi.fn() })
}))

const store = reactive({
  currentVersion: '0.2.17-custom.12',
  latestVersion: '0.2.17-custom.13',
  hasUpdate: true,
  sourceUpdateEnabled: true,
  hostUpdateManaged: true,
  buildType: 'release',
  versionLoading: false,
  versionLoaded: true,
  versionWarning: '',
  releaseInfo: null,
  fetchVersion: vi.fn(),
  clearVersionCache: vi.fn()
})
const jobKey = 'sub2api-source-update-job'
let wrapper: ReturnType<typeof mount>

async function openMenu() {
  wrapper = mount(VersionBadge, { global: { stubs: { Icon: true, Transition: true } } })
  await wrapper.get('button').trigger('click')
  await flushPromises()
}

beforeEach(() => {
  vi.useFakeTimers()
  vi.resetAllMocks()
  localStorage.clear()
  Object.assign(store, { hasUpdate: true, sourceUpdateEnabled: true, hostUpdateManaged: true, buildType: 'release', versionLoaded: true, versionLoading: false, versionWarning: '' })
  vi.mocked(getSourceUpdateStatus).mockResolvedValue({ job_id: 'job-1', status: 'running', stage: 'pulling' })
  vi.mocked(performUpdate).mockResolvedValue({ message: 'queued', source_update: true, need_restart: false, job: { job_id: 'job-1', status: 'queued' } })
})

afterEach(async () => {
  wrapper?.unmount()
  await flushPromises()
  vi.useRealTimers()
  localStorage.clear()
})

describe('host image update', () => {
  it('shows failed checks instead of claiming the installed version is latest', async () => {
    store.hasUpdate = false
    store.versionWarning = 'read release readiness manifest: HTTP 504'
    await openMenu()
    expect(wrapper.text()).toContain('version.checkFailed')
    expect(wrapper.get('[role="alert"]').text()).toContain('HTTP 504')
    expect(wrapper.text()).not.toContain('version.upToDate')
    expect(wrapper.find('[data-testid="version-up-to-date"]').exists()).toBe(false)
    expect(wrapper.get('button').attributes('title')).toBe('version.checkFailed')
    await wrapper.get('button[title="version.refresh"]').trigger('click')
    expect(store.fetchVersion).toHaveBeenLastCalledWith(true)
  })

  it('only confirms the latest version after a successful check', async () => {
    store.hasUpdate = false
    store.versionLoaded = false
    await openMenu()
    expect(wrapper.text()).toContain('version.notChecked')
    expect(wrapper.find('[data-testid="version-up-to-date"]').exists()).toBe(false)
    store.versionLoaded = true
    await flushPromises()
    expect(wrapper.text()).toContain('version.upToDate')
    expect(wrapper.find('[data-testid="version-up-to-date"]').exists()).toBe(true)
  })

  it('revalidates cached status whenever the menu is opened', async () => {
    await openMenu()
    store.fetchVersion.mockClear()
    await wrapper.get('button').trigger('click')
    expect(store.fetchVersion).not.toHaveBeenCalled()
    await wrapper.get('button').trigger('click')
    expect(store.fetchVersion).toHaveBeenCalledWith(false)
  })

  it('updates release containers through the host and keeps progress visible if version data changes', async () => {
    await openMenu()
    expect(wrapper.text()).not.toContain('version.rollback')
    const button = wrapper.findAll('button').find(b => b.text() === 'version.updateNow')!
    void button.trigger('click')
    await flushPromises()
    expect(performUpdate).toHaveBeenCalledOnce()
    expect(getSourceUpdateStatus).toHaveBeenCalledWith('job-1')
    expect(localStorage.getItem(jobKey)).toBe('job-1')
    expect(wrapper.text()).toContain('version.sourceStages.pulling')
    store.hasUpdate = false
    await flushPromises()
    expect(wrapper.text()).toContain('version.sourceStages.pulling')
    expect(wrapper.findAll('button').find(b => b.text() === 'version.updating')?.attributes('disabled')).toBeDefined()
  })

  it('shows the offline host hint without offering a binary update or rollback', async () => {
    store.sourceUpdateEnabled = false
    await openMenu()
    expect(wrapper.text()).toContain('version.hostUpdaterUnavailable')
    expect(wrapper.text()).not.toContain('version.updateNow')
    expect(wrapper.text()).not.toContain('version.rollback')
    expect(performUpdate).not.toHaveBeenCalled()
  })

  it('resumes after a reload and temporary downtime, then reports rollback failure details', async () => {
    localStorage.setItem(jobKey, 'pending-job')
    vi.mocked(getSourceUpdateStatus)
      .mockRejectedValueOnce(new Error('container restarting'))
      .mockResolvedValueOnce({ job_id: 'pending-job', status: 'running', stage: 'rolling_back' })
      .mockResolvedValueOnce({ job_id: 'pending-job', status: 'failed', error: 'automatic rollback incomplete: unhealthy' })
    await openMenu()
    expect(performUpdate).not.toHaveBeenCalled()
    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.text()).toContain('version.sourceStages.rolling_back')
    await vi.advanceTimersByTimeAsync(2000)
    expect(wrapper.text()).toContain('automatic rollback incomplete: unhealthy')
    expect(localStorage.getItem(jobKey)).toBeNull()
  })

  it('stops polling when unmounted and keeps the job available for the next page', async () => {
    localStorage.setItem(jobKey, 'pending-job')
    await openMenu()
    expect(getSourceUpdateStatus).toHaveBeenCalledOnce()
    wrapper.unmount()
    await vi.advanceTimersByTimeAsync(10_000)
    expect(getSourceUpdateStatus).toHaveBeenCalledOnce()
    expect(localStorage.getItem(jobKey)).toBe('pending-job')
    expect(vi.getTimerCount()).toBe(0)
  })
})
