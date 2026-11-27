import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
import IntelligencePolicyPanel from '@/components/tests/IntelligencePolicyPanel.vue'
import QualityOperationsPanel from '@/components/tests/QualityOperationsPanel.vue'

const api = vi.hoisted(() => ({
  plans: vi.fn(), groups: vi.fn(), accounts: vi.fn(), listTypes: vi.fn(),
  getVisibility: vi.fn(), setVisibility: vi.fn(),
  refreshIntelligence: vi.fn(), refreshOperations: vi.fn(),
}))
vi.mock('@/api/admin/intelligence', () => ({ getIntelligenceVisibility: api.getVisibility, setIntelligenceVisibility: api.setVisibility }))
vi.mock('@/api/admin', () => ({
  adminAPI: { tests: { listPlans: api.plans, listTypes: api.listTypes }, groups: { getAll: api.groups }, accounts: { list: api.accounts } },
}))
vi.mock('@/components/tests/IntelligencePolicyPanel.vue', () => ({
  default: { props: ['groups', 'accounts', 'plans', 'catalogReady', 'initialPlanId'], emits: ['changed'], template: '<div />', setup: () => ({ refresh: api.refreshIntelligence }) },
}))
vi.mock('@/components/tests/QualityOperationsPanel.vue', () => ({
  default: { props: ['accounts', 'visible'], template: '<div />', setup: () => ({ refresh: api.refreshOperations }) },
}))

async function render(url: string) {
  const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/admin/tests', component: TestManagementView }] })
  await router.push(url)
  await router.isReady()
  const wrapper = mount(TestManagementView, { global: {
    plugins: [router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })],
    stubs: { AppLayout: { template: '<main><slot /></main>' }, Icon: true },
  } })
  await flushPromises()
  return { wrapper, router }
}

beforeEach(() => {
  vi.resetAllMocks()
  api.getVisibility.mockResolvedValue(false)
  api.setVisibility.mockImplementation(async enabled => enabled)
  api.plans.mockResolvedValue([{ id: 5, protection: { intelligence: {} } }])
  api.groups.mockResolvedValue([{ id: 7, name: 'Plus' }])
  api.accounts.mockImplementation(async (page: number) => page === 1
    ? { items: [{ id: 62 }], pages: 2, total: 2 }
    : { items: [{ id: 63, status: 'disabled' }], pages: 2, total: 2 })
})

describe('intelligence management navigation', () => {
  it('loads the saved publication switch and only changes it after a successful save', async () => {
    const { wrapper } = await render('/admin/tests')
    const toggle = wrapper.get('[role=switch]')
    expect(toggle.attributes('aria-checked')).toBe('false')
    await toggle.trigger('click')
    await flushPromises()
    expect(api.setVisibility).toHaveBeenCalledWith(true)
    expect(toggle.attributes('aria-checked')).toBe('true')
    api.setVisibility.mockRejectedValueOnce(new Error('Save failed'))
    await toggle.trigger('click')
    await flushPromises()
    expect(wrapper.get('[role=alert]').text()).toContain('Save failed')
    expect(toggle.attributes('aria-checked')).toBe('true')
    await toggle.trigger('click')
    await flushPromises()
    expect(api.setVisibility).toHaveBeenLastCalledWith(false)
    expect(toggle.attributes('aria-checked')).toBe('false')
    wrapper.unmount()
  })
  it('loads the catalog independently of retired tabs and keeps direct result links', async () => {
    const { wrapper, router } = await render('/admin/tests?tab=plans&plan_id=5')
    expect(wrapper.findAll('[role="tab"]').map(tab => tab.attributes('data-quality-section'))).toEqual(['intelligence', 'operations'])
    expect(wrapper.getComponent(IntelligencePolicyPanel).props()).toMatchObject({
      initialPlanId: 5, groups: [{ id: 7 }], plans: [{ id: 5 }],
      accounts: [{ id: 62 }, { id: 63, status: 'disabled' }], catalogReady: true,
    })
    for (const tab of ['plans', 'types']) {
      await router.replace({ query: { tab } })
      await flushPromises()
      expect(wrapper.get('[data-quality-section="intelligence"]').attributes('aria-selected')).toBe('true')
    }
    await wrapper.get('[data-quality-section="operations"]').trigger('click')
    await flushPromises()
    expect(wrapper.getComponent(QualityOperationsPanel).props('accounts')).toEqual([{ id: 62 }, { id: 63, status: 'disabled' }])
    await wrapper.get('header button').trigger('click')
    await flushPromises()
    expect(api.refreshOperations).toHaveBeenCalledOnce()
    expect(api.listTypes).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('retries a failed catalog and keeps the latest list after overlapping refreshes', async () => {
    api.plans.mockRejectedValueOnce(new Error('Could not load plans'))
    const { wrapper } = await render('/admin/tests')
    expect(wrapper.get('[role="alert"]').text()).toContain('Could not load plans')
    expect(wrapper.findComponent(IntelligencePolicyPanel).exists()).toBe(false)
    await wrapper.get('header button').trigger('click')
    await flushPromises()
    const panel = wrapper.getComponent(IntelligencePolicyPanel)
    expect(panel.props('catalogReady')).toBe(true)
    let resolveOlder!: (plans: { id: number }[]) => void
    api.plans.mockReturnValueOnce(new Promise(resolve => { resolveOlder = resolve }))
    panel.vm.$emit('changed')
    api.plans.mockResolvedValueOnce([{ id: 7 }])
    panel.vm.$emit('changed')
    await flushPromises()
    resolveOlder([{ id: 6 }])
    await flushPromises()
    expect(panel.props('plans')).toEqual([{ id: 7 }])
    wrapper.unmount()
  })
})
