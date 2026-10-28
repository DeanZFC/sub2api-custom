import { reactive } from 'vue'
import { mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import TestManagementView from '../TestManagementView.vue'
import resources from '@/i18n/locales/en/admin/resources'
const navigation = vi.hoisted(() => ({ route: { query: {} as Record<string, string> }, replace: vi.fn() }))
vi.mock('vue-router', () => ({ useRoute: () => navigation.route, useRouter: () => ({ replace: navigation.replace }) }))
const mountView = () => mount(TestManagementView, { global: {
  plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })],
  stubs: {
    AppLayout: { template: '<div><slot /></div>' }, Icon: true,
    ActiveQualityPanel: true, PassiveQualityPanel: true, ChannelQualityScoreSettings: true, ChannelQualityOverview: true, QualityOperationsPanel: true,
    PassiveQualityManagementPanel: { props: ['targetPolicyId', 'advancedPolicy'], template: '<div data-management :data-policy="targetPolicyId" :data-advanced="advancedPolicy" />' },
  },
} })
beforeEach(() => { navigation.route = reactive({ query: {} }); navigation.replace.mockReset() })
describe('simplified quality configuration flows', () => {
  it('keeps legacy group-target deep links under passive management', () => {
    navigation.route.query = { tab: 'group-targets', policy_id: '41' }
    const wrapper = mountView()
    expect(wrapper.findAll('[role="tab"]')).toHaveLength(4)
    expect(wrapper.get('[data-quality-section="group-targets"]').attributes('aria-selected')).toBe('true')
    expect(wrapper.get('[data-management]').attributes('data-policy')).toBe('41')
    expect(wrapper.get('[data-management]').attributes('data-advanced')).toBe('false')
    wrapper.unmount()
  })
  it('opens legacy individual passive policies in the advanced section and preserves active navigation', async () => {
    navigation.route.query = { tab: 'passive', policy_id: '7' }
    const wrapper = mountView()
    expect(wrapper.find('[data-management]').exists()).toBe(false)
    expect(wrapper.get('[data-quality-tab="legacy"]').attributes('aria-pressed')).toBe('true')
    expect(wrapper.findComponent({ name: 'PassiveQualityPanel' }).props('tab')).toBe('passive')
    await wrapper.get('[data-quality-section="plans"]').trigger('click')
    expect(navigation.replace).toHaveBeenCalledWith({ query: { tab: 'plans' } })
    wrapper.unmount()
  })
  it('retains account filters when entering the score overview', () => {
    navigation.route.query = { tab: 'overview', account_id: '19' }
    const wrapper = mountView()
    expect(wrapper.findComponent({ name: 'ChannelQualityOverview' }).props('accountId')).toBe(19)
    wrapper.unmount()
  })
})
