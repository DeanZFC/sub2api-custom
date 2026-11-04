import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { reactive, ref } from 'vue'
import CodexTicketFeatureSettings from '../CodexTicketFeatureSettings.vue'

const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), refresh: vi.fn(), success: vi.fn() }))
const app = reactive({
  cachedPublicSettings: { openai_codex_ticket_enabled: false },
  fetchPublicSettings: mocks.refresh,
  showSuccess: mocks.success,
})
vi.mock('@/api/admin/settings', () => ({ getSettings: mocks.get, updateSettings: mocks.update }))
vi.mock('@/stores/app', () => ({ useAppStore: () => app }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('zh') }) }))

beforeEach(() => {
  vi.clearAllMocks()
  app.cachedPublicSettings = { openai_codex_ticket_enabled: false }
  mocks.get.mockResolvedValue({})
  mocks.update.mockImplementation(async body => body)
})

describe('Codex ticket master switch', () => {
  it('starts disabled when no value has been configured', async () => {
    const wrapper = mount(CodexTicketFeatureSettings)
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.text()).toContain('默认关闭')
    wrapper.unmount()
  })

  it('saves only the master switch and immediately updates menu visibility', async () => {
    const wrapper = mount(CodexTicketFeatureSettings)
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith({ openai_codex_ticket_enabled: true })
    expect(app.cachedPublicSettings.openai_codex_ticket_enabled).toBe(true)
    expect(mocks.refresh).toHaveBeenCalledWith(true)
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenLastCalledWith({ openai_codex_ticket_enabled: false })
    expect(app.cachedPublicSettings.openai_codex_ticket_enabled).toBe(false)
    wrapper.unmount()
  })

  it('preserves visible state if saving fails', async () => {
    mocks.update.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(CodexTicketFeatureSettings)
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(app.cachedPublicSettings.openai_codex_ticket_enabled).toBe(false)
    expect(wrapper.get('[role="alert"]').text()).toContain('未保存')
    expect(mocks.refresh).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('prevents saving if the current setting cannot be loaded', async () => {
    mocks.get.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(CodexTicketFeatureSettings)
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('无法读取')
    expect(mocks.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
