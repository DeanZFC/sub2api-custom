import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { reactive } from 'vue'
import Panel from './MihomoProxySelector.vue'

const { get } = vi.hoisted(() => ({ get: vi.fn() }))
const appStore = reactive({ cachedPublicSettings: undefined as { openai_codex_ticket_enabled?: boolean } | undefined })
vi.mock('@/stores/app', () => ({ useAppStore: () => appStore }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ locale: { value: 'zh' } }) }))
enableAutoUnmount(afterEach)
const mountPanel = () => mount(Panel, { global: { stubs: { RouterLink: { template: '<a><slot /></a>' } } } })
beforeEach(() => {
  vi.clearAllMocks()
  appStore.cachedPublicSettings = undefined
  get.mockResolvedValue({ data: { running: true, busy: false, nodes: 1, endpoint: 'http://127.0.0.1:3101' } })
})

describe('Mihomo harvest proxy entry', () => {
  it.each([undefined, false])('hides the selector when enabled is %s', enabled => {
    appStore.cachedPublicSettings = enabled === undefined ? undefined : { openai_codex_ticket_enabled: enabled }
    const wrapper = mountPanel()
    expect(wrapper.find('button').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('设为打票代理')
    expect(get).not.toHaveBeenCalled()
  })

  it('verifies and selects Mihomo only when ticket harvesting is enabled', async () => {
    appStore.cachedPublicSettings = { openai_codex_ticket_enabled: true }
    const wrapper = mountPanel()
    await wrapper.get('button').trigger('click')
    await flushPromises()
    expect(get).toHaveBeenCalledWith('/admin/system/mihomo')
    expect(wrapper.emitted('ready')).toEqual([['http://127.0.0.1:3101']])
    appStore.cachedPublicSettings.openai_codex_ticket_enabled = false
    await flushPromises()
    expect(wrapper.find('button').exists()).toBe(false)
  })

  it('ignores a pending selection when ticket harvesting is disabled', async () => {
    let finish!: (result: unknown) => void
    get.mockReturnValue(new Promise(resolve => { finish = resolve }))
    appStore.cachedPublicSettings = { openai_codex_ticket_enabled: true }
    const wrapper = mountPanel()
    await wrapper.get('button').trigger('click')
    appStore.cachedPublicSettings.openai_codex_ticket_enabled = false
    finish({ data: { running: true, busy: false, nodes: 1, endpoint: 'http://127.0.0.1:3101' } })
    await flushPromises()
    expect(wrapper.emitted('ready')).toBeUndefined()
  })
})
