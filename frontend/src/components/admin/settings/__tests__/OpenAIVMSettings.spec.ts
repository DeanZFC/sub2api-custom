import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIVMSettings from '../OpenAIVMSettings.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import type { OpenAIVMConfig } from '@/api/admin/openaiVm'
import type { StepUpController } from '@/composables/useStepUp'

const api = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), testConnection: vi.fn(), getBinding: vi.fn(),
  createBinding: vi.fn(), retryBinding: vi.fn(), deleteBinding: vi.fn()
}))
vi.mock('@/api/admin/openaiVm', () => ({ openaiVmAPI: api }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const config: OpenAIVMConfig = { enabled: true, base_url: 'https://bridge.example', proxy_url: 'https://proxy.example', secret_configured: true }

function mountSettings() {
  return mount(OpenAIVMSettings, {
    global: { stubs: {
      TotpStepUpDialog: { props: ['controller'], template: '<div />' },
      Icon: true
    } }
  })
}

function deferred<T>() {
  let resolve!: (value: T) => void
  let reject!: (reason: unknown) => void
  const promise = new Promise<T>((res, rej) => { resolve = res; reject = rej })
  return { promise, resolve, reject }
}

describe('OpenAI dedicated slot global settings', () => {
  beforeEach(() => {
    Object.values(api).forEach(fn => fn.mockReset())
    api.getConfig.mockResolvedValue(config)
    api.saveConfig.mockResolvedValue(config)
    api.testConnection.mockResolvedValue({ healthy: true })
  })

  it('loads global configuration without an account and omits the blank secret on save', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    expect(wrapper.text()).toContain('admin.settings.openaiVm.description')
    expect(wrapper.get<HTMLInputElement>('#openai-vm-secret').element.value).toBe('')
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith({ enabled: true, base_url: config.base_url, proxy_url: config.proxy_url })
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.saved')
    expect(api.getBinding).not.toHaveBeenCalled()
    expect(api.createBinding).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('requires saving edits before health testing and clears a replacement secret after success', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('#openai-vm-base-url').setValue('  https://next-bridge.example  ')
    await wrapper.get('#openai-vm-secret').setValue('replacement-secret')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-test"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="vm-test"]').trigger('click')
    expect(api.testConnection).not.toHaveBeenCalled()
    const saved = { ...config, base_url: 'https://next-bridge.example' }
    api.saveConfig.mockResolvedValueOnce(saved)
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith({ enabled: true, base_url: saved.base_url, proxy_url: config.proxy_url, secret: 'replacement-secret' })
    expect(wrapper.get<HTMLInputElement>('#openai-vm-secret').element.value).toBe('')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-test"]').element.disabled).toBe(false)
    wrapper.unmount()
  })

  it('tests saved configuration without creating or changing any account binding', async () => {
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-test"]').trigger('click')
    await flushPromises()
    expect(api.testConnection).toHaveBeenCalledOnce()
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.healthy')
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(api.getBinding).not.toHaveBeenCalled()
    expect(api.createBinding).not.toHaveBeenCalled()
    expect(api.retryBinding).not.toHaveBeenCalled()
    expect(api.deleteBinding).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('reports an unhealthy response without claiming health-check success', async () => {
    api.testConnection.mockResolvedValueOnce({ healthy: false, message: 'Bridge unavailable' })
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-test"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('Bridge unavailable')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.healthy')
    wrapper.unmount()
  })

  it('offers a load retry without exposing an editable fallback configuration', async () => {
    api.getConfig.mockRejectedValueOnce(new Error('Cannot load configuration'))
    const wrapper = mountSettings()
    await flushPromises()
    expect(wrapper.text()).toContain('Cannot load configuration')
    expect(wrapper.find('[data-testid="vm-config-form"]').exists()).toBe(false)
    await wrapper.get('[data-testid="vm-config-retry"]').trigger('click')
    await flushPromises()
    expect(api.getConfig).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[data-testid="vm-config-form"]').exists()).toBe(true)
    expect(wrapper.text()).not.toContain('Cannot load configuration')
    wrapper.unmount()
  })

  it('preserves edits and the server error when existing bindings prevent a save', async () => {
    api.saveConfig.mockRejectedValueOnce(new Error('Unbind all accounts before changing configuration'))
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('#openai-vm-secret').setValue('replacement-secret')
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain('Unbind all accounts before changing configuration')
    expect(wrapper.get<HTMLInputElement>('#openai-vm-secret').element.value).toBe('replacement-secret')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.saved')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-test"]').element.disabled).toBe(true)
    wrapper.unmount()
  })

  it.each([{ enabled: false }, { secret_configured: false }])('disables health testing for incomplete saved configuration: %s', async (override) => {
    api.getConfig.mockResolvedValueOnce({ ...config, ...override })
    const wrapper = mountSettings()
    await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-test"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="vm-test"]').trigger('click')
    expect(api.testConnection).not.toHaveBeenCalled()
    wrapper.unmount()
  })

  it('blocks duplicate submissions and other actions while saving', async () => {
    const pending = deferred<OpenAIVMConfig>()
    api.saveConfig.mockReturnValueOnce(pending.promise)
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await wrapper.get('[data-testid="vm-test"]').trigger('click')
    expect(api.saveConfig).toHaveBeenCalledOnce()
    expect(api.testConnection).not.toHaveBeenCalled()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-save"]').element.disabled).toBe(true)
    pending.resolve(config)
    await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-save"]').element.disabled).toBe(false)
    wrapper.unmount()
  })

  it.each(['save', 'test'] as const)('requires step-up before retrying %s', async (action) => {
    const method = action === 'save' ? api.saveConfig : api.testConnection
    method.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const wrapper = mountSettings()
    await flushPromises()
    if (action === 'save') await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    else await wrapper.get('[data-testid="vm-test"]').trigger('click')
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller') as StepUpController
    expect(controller.visible.value).toBe(true)
    expect(method).toHaveBeenCalledTimes(1)
    controller.onVerified()
    await flushPromises()
    expect(method).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain(action === 'save' ? 'admin.accounts.openaiVm.saved' : 'admin.accounts.openaiVm.healthy')
    wrapper.unmount()
  })

  it.each([
    ['STEP_UP_TOTP_NOT_ENABLED', 'stepUp.notEnabled'],
    ['STEP_UP_ADMIN_API_KEY_FORBIDDEN', 'stepUp.adminApiKeyForbidden']
  ])('reports blocked verification for %s', async (code, message) => {
    api.saveConfig.mockRejectedValueOnce({ code })
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    expect(wrapper.text()).toContain(message)
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.saved')
    expect(api.saveConfig).toHaveBeenCalledOnce()
    wrapper.unmount()
  })

  it('cancels verification without retrying or hiding the previous operation error', async () => {
    api.saveConfig.mockRejectedValueOnce(new Error('First save failed'))
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    api.saveConfig.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller') as StepUpController
    controller.onCancel()
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('First save failed')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.saved')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-save"]').element.disabled).toBe(false)
    wrapper.unmount()
  })

  it('cancels a pending verification prompt when leaving settings', async () => {
    api.saveConfig.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller') as StepUpController
    expect(controller.visible.value).toBe(true)
    wrapper.unmount()
    controller.onVerified()
    await flushPromises()
    expect(controller.visible.value).toBe(false)
    expect(api.saveConfig).toHaveBeenCalledOnce()
  })

  it('does not submit a verification retry if unmounted immediately after approval', async () => {
    api.saveConfig.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const wrapper = mountSettings()
    await flushPromises()
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller') as StepUpController
    controller.onVerified()
    wrapper.unmount()
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledOnce()
  })

  it('does not reopen verification for a late response after leaving settings', async () => {
    const pending = deferred<OpenAIVMConfig>()
    api.saveConfig.mockReturnValueOnce(pending.promise)
    const wrapper = mountSettings()
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller') as StepUpController
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    wrapper.unmount()
    pending.reject({ code: 'STEP_UP_REQUIRED' })
    await flushPromises()
    expect(controller.visible.value).toBe(false)
    expect(api.saveConfig).toHaveBeenCalledOnce()
  })

  it('does not apply a previous instance late configuration response to the reopened panel', async () => {
    const pending = deferred<OpenAIVMConfig>()
    api.getConfig.mockReturnValueOnce(pending.promise)
    const previous = mountSettings()
    previous.unmount()
    const current = mountSettings()
    await flushPromises()
    pending.resolve({ ...config, base_url: 'https://outdated-bridge.example' })
    await flushPromises()
    expect(current.get<HTMLInputElement>('#openai-vm-base-url').element.value).toBe(config.base_url)
    expect(current.get<HTMLInputElement>('#openai-vm-secret').element.value).toBe('')
    current.unmount()
  })
})
