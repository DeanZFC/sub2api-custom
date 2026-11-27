import { describe, expect, it, vi, beforeEach } from 'vitest'
import { mount } from '@vue/test-utils'
import { createI18n } from 'vue-i18n'
import type { IntelligenceGroup } from '@/api/intelligence'
import zh from '@/i18n/locales/zh/channelMonitorV2'
import ChannelStatusView from '../ChannelStatusView.vue'

const state = vi.hoisted(() => ({
  v1: false,
  traffic: true,
  query: {} as Record<string, string>,
  directory: { groups: [] as IntelligenceGroup[], load: vi.fn(), loading: false, failed: false },
}))
vi.mock('@/utils/featureFlags', () => ({
  isChannelMonitorV1Mode: () => state.v1,
  isChannelMonitorRouteEnabled: () => state.traffic,
}))
vi.mock('@/stores/intelligenceGroups', () => ({ useIntelligenceGroupsStore: () => state.directory }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ user: { id: 1 } }) }))
vi.mock('vue-router', async () => ({
  ...(await vi.importActual('vue-router')),
  useRoute: () => ({ query: state.query }),
  useRouter: () => ({ replace: vi.fn() }),
}))

function render() {
  return mount(ChannelStatusView, {
    global: {
      plugins: [createI18n({ legacy: false, locale: 'zh', missingWarn: false, fallbackWarn: false, messages: { zh } })],
      stubs: {
        AppLayout: { template: '<main><slot /></main>' },
        ChannelStatusV1View: true,
        ChannelStatusV2View: true,
        RouterLink: true,
        IntelligenceRounds: true,
      },
    },
  })
}
describe('channel status visibility', () => {
  beforeEach(() => {
    state.v1 = false
    state.traffic = true
    state.query = {}
    state.directory.groups = []
  })
  it.each([false, true])('selects the configured traffic view (v1 %s)', (v1) => {
    state.v1 = v1
    const wrapper = render()
    expect(wrapper.find('channel-status-v1-view-stub').exists()).toBe(v1)
    expect(wrapper.find('channel-status-v2-view-stub').exists()).toBe(!v1)
    wrapper.unmount()
  })
  it('shows entitled intelligence groups without a traffic entitlement or any measured traffic', () => {
    state.traffic = false
    state.directory.groups = [{ id: 7, name: 'Pro', models: ['m1'] }]
    const wrapper = render()
    expect(wrapper.get('intelligence-rounds-stub').exists()).toBe(true)
    expect(wrapper.find('channel-status-v2-view-stub').exists()).toBe(false)
    wrapper.unmount()
  })
  it('restores only authorized group and model selections from a bookmark', () => {
    state.query = { section: 'intelligence', group: '8', model: 'm2' }
    state.directory.groups = [
      { id: 7, name: 'Pro', models: ['m1'] },
      { id: 8, name: 'Plus', models: ['m2'] },
    ]
    const wrapper = render()
    expect(wrapper.findAllComponents({ name: 'IntelligenceRounds' }).map((w) => [w.props('group').id, w.props('model')])).toEqual(
      [[8, 'm2']],
    )
    wrapper.unmount()
  })
})
