import { defaultPassivePolicy } from '@/test/fixtures/passiveQuality'
import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
import { emptyPassiveSummary } from '@/api/admin/passiveQuality'
import resources from '@/i18n/locales/en/admin/resources'

const api = vi.hoisted(() => ({ policies: vi.fn(), stateSummary: vi.fn(), statePage: vi.fn(), eventPage: vi.fn(), models: vi.fn(), success: vi.fn(), error: vi.fn() }))
vi.mock('@/api/admin/passiveQuality', async original => ({ ...await original<object>(), passiveQualityAPI: api }))
vi.mock('@/api/admin', () => ({ adminAPI: { groups: { getModelAllowlistCandidates: api.models } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess: api.success, showError: api.error }) }))

async function mountView(path: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/admin/tests', component: TestManagementView }] })
  await router.push(path)
  await router.isReady()
  const wrapper = mount(TestManagementView, { global: {
    plugins: [router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
    stubs: {
      AppLayout: { template: '<main><slot /></main>' }, Icon: true,
      ActiveQualityPanel: true, ChannelQualityOverview: true, QualityOperationsPanel: true,
      GroupQualityTargetsPanel: true,
      BaseDialog: true,
    },
  } })
  await flushPromises()
  return { wrapper, router }
}

beforeEach(() => {
  vi.resetAllMocks()
  api.policies.mockResolvedValue([{ ...defaultPassivePolicy(), id: 7, version: 1, name: 'Selected policy', group_ids: [8] }])
  api.stateSummary.mockResolvedValue(emptyPassiveSummary())
  api.statePage.mockResolvedValue({ items: [], total: 0, has_more: false })
  api.eventPage.mockResolvedValue({ items: [], has_more: false })
  api.models.mockResolvedValue([])
})

describe('passive management and records navigation', () => {
  it('preserves the selected target in record deep links', async () => {
    const { wrapper, router } = await mountView('/admin/tests?tab=records&policy_id=7')
    expect(router.currentRoute.value.query).toEqual({ tab: 'records', policy_id: '7' })
    expect((wrapper.get('[data-record-policy]').element as HTMLSelectElement).value).toBe('7')
    expect(api.statePage).toHaveBeenLastCalledWith(expect.objectContaining({ policy_id: 7 }))
    expect(api.eventPage).toHaveBeenLastCalledWith(expect.objectContaining({ policy_id: 7 }))
    expect(wrapper.find('[data-passive-management]').exists()).toBe(false)
    wrapper.unmount()
  })

  it.each([
    ['pausedAccounts', 'paused'], ['limitedAccounts', 'limited'],
    ['observedAccounts', 'observed'],
  ] as const)('preserves the %s summary filter across component replacement', async (key, phase) => {
    const { wrapper, router } = await mountView('/admin/tests?tab=records')
    await wrapper.get('[data-passive-quality]').findAll('button').find(button => button.text().includes(resources.tests.passive[key]))!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'records', phase })
    expect((wrapper.get('[data-record-phase]').element as HTMLSelectElement).value).toBe(phase)
    expect(api.statePage).toHaveBeenLastCalledWith(expect.objectContaining({ phase, policy_id: undefined }))
    wrapper.unmount()
  })

  it('opens group targets from the active-policy summary', async () => {
    const { wrapper, router } = await mountView('/admin/tests?tab=records&phase=paused')
    await wrapper.get('[data-passive-quality]').findAll('button').find(button => button.text().includes(resources.tests.passive.activePolicies))!.trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'group-targets' })
    expect(wrapper.find('[data-quality-tab="legacy"]').exists()).toBe(false)
    expect(wrapper.find('[data-passive-management]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('updates record phases from route changes and rejects unsupported values', async () => {
    const { wrapper, router } = await mountView('/admin/tests?tab=records&phase=paused')
    expect(api.statePage).toHaveBeenLastCalledWith(expect.objectContaining({ phase: 'paused' }))
    await router.replace('/admin/tests?tab=records&phase=probing'); await flushPromises()
    expect(api.statePage).toHaveBeenLastCalledWith(expect.objectContaining({ phase: 'probing' }))
    await router.replace('/admin/tests?tab=records&phase=trial'); await flushPromises()
    expect(api.statePage).toHaveBeenLastCalledWith(expect.objectContaining({ phase: undefined }))
    await router.replace('/admin/tests?tab=records&phase=unexpected'); await flushPromises()
    expect((wrapper.get('[data-record-phase]').element as HTMLSelectElement).value).toBe('')
    expect(api.statePage).toHaveBeenLastCalledWith(expect.objectContaining({ phase: undefined }))
    wrapper.unmount()
  })
})
