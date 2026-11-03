import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'
import MihomoSettings from '../settings/MihomoSettings.vue'

const { listProxies, getAllWithCount, route, replace } = vi.hoisted(() => ({
  listProxies: vi.fn(), getAllWithCount: vi.fn(), route: { hash: '' }, replace: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list: listProxies, getAllWithCount } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn() }) }))
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

  it('preserves the existing Mihomo hash entry', () => {
    route.hash = '#mihomo'
    wrapper = mountView()
    expect(wrapper.getComponent(MihomoSettings).props('section')).toBe('subscriptions')
  })
})
