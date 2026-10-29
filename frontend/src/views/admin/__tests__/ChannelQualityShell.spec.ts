import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
const refresh = vi.fn()
vi.mock('@/components/tests/ActiveQualityPanel.vue', () => ({ default: { props: ['tab'], template: '<div data-active :data-tab="tab" />', setup: () => ({ refresh }) } }))
vi.mock('@/components/tests/QualityOperationsPanel.vue', () => ({ default: { template: '<div data-operations />' } }))
describe('active intelligence management navigation', () => {
  it('defaults to active checks, retains active deep links, and replaces retired sections', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/admin/tests', component: TestManagementView }] })
    await router.push('/admin/tests?plan_id=5&account_id=62'); await router.isReady()
    const wrapper = mount(TestManagementView, { global: { plugins: [router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })], stubs: { AppLayout: { template: '<main><slot/></main>' }, Icon: true } } })
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(3)
    expect(wrapper.get('[data-active]').isVisible()).toBe(true)
    expect(wrapper.get('[data-quality-section="plans"]').attributes('aria-selected')).toBe('true')
    expect(router.currentRoute.value.query).toEqual({ plan_id: '5', account_id: '62' })
    for (const tab of ['passive', 'group-targets', 'records', 'overview', 'scoring', 'legacy']) {
      await router.replace({ path: '/admin/tests', query: { tab, policy_id: '7' } }); await flushPromises()
      expect(wrapper.get('[data-active]').attributes('data-tab')).toBe('plans')
      expect(wrapper.get('[data-quality-section="plans"]').attributes('aria-selected')).toBe('true')
      expect(wrapper.find('[data-quality-section="group-targets"]').exists()).toBe(false)
    }
    await wrapper.get('[data-quality-section="types"]').trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'types' })
    expect(wrapper.get('[data-active]').attributes('data-tab')).toBe('types')
    await wrapper.get('[data-quality-section="operations"]').trigger('click'); await flushPromises()
    expect(wrapper.get('[data-operations]').isVisible()).toBe(true)
    expect(wrapper.get('[data-active]').attributes('style')).toContain('display: none')
    wrapper.unmount()
  })
})
