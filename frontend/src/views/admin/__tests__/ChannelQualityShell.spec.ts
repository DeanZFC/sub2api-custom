import { flushPromises, mount } from '@vue/test-utils'
import { describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
const refresh = vi.fn()
vi.mock('@/components/tests/ActiveQualityPanel.vue', () => ({ default: { template: '<div data-active />', setup: () => ({ refresh }) } }))
vi.mock('@/components/tests/QualityOperationsPanel.vue', () => ({ default: { template: '<div data-operations />' } }))
vi.mock('@/components/tests/PassiveQualityPanel.vue', () => ({ default: { template: '<div data-passive />' } }))
vi.mock('@/components/tests/ChannelQualityOverview.vue', () => ({ default: { props: ['accountId'], template: '<button data-overview @click="$emit(\'navigate\', \'plans\', 5, accountId)">Inspect</button>' } }))
describe('quality management navigation', () => {
  it('opens overview by default and deep-links to the selected policy', async () => {
    const router = createRouter({ history: createMemoryHistory(), routes: [{ path: '/admin/tests', component: TestManagementView }] })
    await router.push('/admin/tests?account_id=62'); await router.isReady()
    const w = mount(TestManagementView, { global: { plugins: [router, createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: {} })], stubs: { AppLayout: { template: '<main><slot/></main>' }, Icon: true } } })
    expect(w.findAll('[role="tab"]')).toHaveLength(4); expect(w.get('[data-overview]').isVisible()).toBe(true)
    await w.get('[data-overview]').trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'plans', plan_id: '5', account_id: '62' }); expect(w.get('[data-active]').isVisible()).toBe(true)
    expect(w.get('[data-quality-tab="plans"]').attributes('aria-pressed')).toBe('true')
    await w.get('[data-quality-tab="passive"]').trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'passive' })
    expect(w.get('[data-passive]').isVisible()).toBe(true)
    await router.replace('/admin/tests?tab=operations'); await flushPromises()
    expect(w.get('[data-operations]').isVisible()).toBe(true)
    expect(w.get('[data-quality-tab="operations"]').attributes('aria-pressed')).toBe('true')
    await w.get('[data-quality-tab="types"]').trigger('click'); await flushPromises()
    expect(router.currentRoute.value.query).toEqual({ tab: 'types' })
    w.unmount()
  })
})
