import { beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import GPTDirectSettings from '../GPTDirectSettings.vue'

const mocks = vi.hoisted(() => ({ settings: vi.fn(), saveSettings: vi.fn(), fetchPublicSettings: vi.fn() }))
vi.mock('@/api/gptDirect', () => ({ gptDirectAPI: mocks }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ fetchPublicSettings: mocks.fetchPublicSettings }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

const configured = { enabled: true, base_url: 'https://recharge.example.com', tenant_slug: 'ctai', purchase_url: 'https://shop.example.com', button_text: '', description: '', support_url: '' }

describe('GPT Direct settings', () => {
  beforeEach(() => {
    vi.clearAllMocks()
    mocks.settings.mockResolvedValue(configured)
    mocks.saveSettings.mockImplementation(async value => value)
    mocks.fetchPublicSettings.mockResolvedValue({ gpt_direct_enabled: false })
  })
  it('loads the saved switch and saves independently, then refreshes menu visibility', async () => {
    const wrapper = mount(GPTDirectSettings)
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.saveSettings).toHaveBeenCalledWith({ ...configured, enabled: false })
    expect(mocks.fetchPublicSettings).toHaveBeenCalledWith(true)
    wrapper.unmount()
  })
  it('does not overwrite configured settings with defaults if loading fails', async () => {
    mocks.settings.mockRejectedValue(new Error('offline'))
    const wrapper = mount(GPTDirectSettings)
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    await wrapper.get('button.btn-primary').trigger('click')
    expect(mocks.saveSettings).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('gptDirect.loadFailed')
    wrapper.unmount()
  })
  it('handles Enter in its own inputs without submitting the general settings form', async () => {
    const wrapper = mount(GPTDirectSettings)
    await flushPromises()
    const event = new KeyboardEvent('keydown', { key: 'Enter', bubbles: true, cancelable: true })
    wrapper.get('input').element.dispatchEvent(event)
    await flushPromises()
    expect(event.defaultPrevented).toBe(true)
    expect(mocks.saveSettings).toHaveBeenCalledWith(configured)
    wrapper.unmount()
  })
})
