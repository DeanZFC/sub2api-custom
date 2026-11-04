import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import { reactive } from 'vue'
import ProxiesView from '../ProxiesView.vue'
import MihomoSettings from '../settings/MihomoSettings.vue'

const { listProxies, getAllWithCount, route, replace } = vi.hoisted(() => ({
  listProxies: vi.fn(), getAllWithCount: vi.fn(), route: { hash: '' }, replace: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list: listProxies, getAllWithCount } } }))
const appStore = reactive({ cachedPublicSettings: undefined as { openai_codex_ticket_enabled?: boolean } | undefined, showError: vi.fn() })
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))
vi.mock('vue-router', () => ({ useRoute: () => route, useRouter: () => ({ replace }) }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const mountView = () => shallowMount(ProxiesView, { global: { stubs: {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="actions" /><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
  DataTable: { template: '<div data-testid="existing-proxy-list" />' },
  Select: true,
  Pagination: true,
  MihomoSettings: true,
} } })
let wrapper: ReturnType<typeof mountView>
beforeEach(() => {
  vi.clearAllMocks()
  route.hash = ''
  appStore.cachedPublicSettings = undefined
  replace.mockResolvedValue(undefined)
  listProxies.mockResolvedValue({ items: [], total: 0, pages: 0 })
  getAllWithCount.mockResolvedValue([])
})
afterEach(() => wrapper?.unmount())

describe('proxy management entries', () => {
  it('starts on the existing proxy list without starting Mihomo', async () => {
    wrapper = mountView()
    await flushPromises()
    expect(wrapper.get('[data-testid="existing-proxy-list"]').isVisible()).toBe(true)
    expect(wrapper.findComponent(MihomoSettings).exists()).toBe(false)
    expect(replace).not.toHaveBeenCalled()
  })

  it.each([
    ['subscriptions', 'admin.proxies.subscriptions'],
    ['dynamic', 'admin.proxies.dynamicProxies'],
    ['nodes', 'admin.proxies.nodes'],
    ['kernel', 'admin.proxies.kernelRules'],
  ])('opens the %s management section from its button', async (section, label) => {
    wrapper = mountView()
    await flushPromises()
    await wrapper.findAll('button').find(button => button.text() === label)!.trigger('click')
    expect(wrapper.getComponent(MihomoSettings).props('section')).toBe(section)
    expect(wrapper.get('[data-testid="existing-proxy-list"]').isVisible()).toBe(false)
    expect(replace).toHaveBeenCalledWith({ hash: `#${section}` })
  })

  it('opens harvest exit settings in IP management when ticket harvesting is enabled', async () => {
    appStore.cachedPublicSettings = { openai_codex_ticket_enabled: true }
    route.hash = '#harvest'
    wrapper = mountView()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'HarvestProxySettings' }).exists()).toBe(true)
    expect(wrapper.findComponent(MihomoSettings).exists()).toBe(false)
  })

  it.each([undefined, false])('hides harvest menus and rejects the harvest hash when enabled is %s', async enabled => {
    appStore.cachedPublicSettings = enabled === undefined ? undefined : { openai_codex_ticket_enabled: enabled }
    route.hash = '#harvest'
    wrapper = mountView()
    await flushPromises()
    expect(wrapper.text()).not.toContain('admin.settings.gatewayForwarding.codexTicketHarvestProxy')
    expect(wrapper.findComponent({ name: 'HarvestProxySettings' }).exists()).toBe(false)
    expect(wrapper.get('[data-testid="existing-proxy-list"]').isVisible()).toBe(true)
    expect(wrapper.findAll('button').some(button => button.text() === 'admin.proxies.kernelRules')).toBe(true)
  })

  it('closes the harvest section when the master switch is disabled', async () => {
    appStore.cachedPublicSettings = { openai_codex_ticket_enabled: true }
    route.hash = '#harvest'
    wrapper = mountView()
    await flushPromises()
    expect(wrapper.findComponent({ name: 'HarvestProxySettings' }).exists()).toBe(true)
    appStore.cachedPublicSettings.openai_codex_ticket_enabled = false
    await flushPromises()
    expect(wrapper.findComponent({ name: 'HarvestProxySettings' }).exists()).toBe(false)
    expect(wrapper.get('[data-testid="existing-proxy-list"]').isVisible()).toBe(true)
  })

  it('refreshes static proxies after the node panel creates them', async () => {
    route.hash = '#nodes'
    wrapper = mountView()
    await flushPromises()
    const before = listProxies.mock.calls.length
    wrapper.findComponent(MihomoSettings).vm.$emit('static-proxies-added')
    await flushPromises()
    expect(listProxies.mock.calls.length).toBe(before + 1)
  })

  it('preserves the existing Mihomo hash entry', () => {
    route.hash = '#mihomo'
    wrapper = mountView()
    expect(wrapper.getComponent(MihomoSettings).props('section')).toBe('subscriptions')
  })
})
