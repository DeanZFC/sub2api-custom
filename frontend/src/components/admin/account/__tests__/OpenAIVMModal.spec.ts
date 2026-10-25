import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIVMModal from '../OpenAIVMModal.vue'
import BaseDialog from '@/components/common/BaseDialog.vue'
import TotpStepUpDialog from '@/components/auth/TotpStepUpDialog.vue'
import type { Account } from '@/types'
import type { OpenAIVMBinding } from '@/api/admin/openaiVm'

const api = vi.hoisted(() => ({
  getConfig: vi.fn(), saveConfig: vi.fn(), testConnection: vi.fn(), getBinding: vi.fn(),
  createBinding: vi.fn(), retryBinding: vi.fn(), deleteBinding: vi.fn()
}))
vi.mock('@/api/admin/openaiVm', () => ({ openaiVmAPI: api }))
vi.mock('vue-i18n', async () => ({
  ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'),
  useI18n: () => ({ t: (key: string) => key })
}))

const config = { enabled: true, base_url: 'https://bridge.example', proxy_url: 'https://proxy.example', secret_configured: true }
const account = { id: 42, name: 'OpenAI parent', platform: 'openai', type: 'oauth', parent_account_id: null } as Account
const binding: OpenAIVMBinding = { account_id: 42, slot_id: 'slot-42', status: 'ready', last_error: null, created_at: '2026-10-24T00:00:00Z', updated_at: '2026-10-24T00:00:00Z' }
const state = (value: OpenAIVMBinding | null = null) => ({ eligible: true, configured: true, binding: value })

function mountModal(value = account) {
  return mount(OpenAIVMModal, {
    props: { show: true, account: value },
    global: { stubs: {
      BaseDialog: { props: ['show', 'showCloseButton', 'closeOnEscape'], emits: ['close'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
      TotpStepUpDialog: { props: ['controller'], template: '<div />' },
      Icon: true
    } }
  })
}

describe('OpenAI dedicated slot modal', () => {
  beforeEach(() => {
    Object.values(api).forEach(fn => fn.mockReset())
    api.getConfig.mockResolvedValue(config)
    api.saveConfig.mockResolvedValue(config)
    api.getBinding.mockResolvedValue(state())
    api.testConnection.mockResolvedValue({ healthy: true })
    api.createBinding.mockResolvedValue(binding)
    api.retryBinding.mockResolvedValue(binding)
    api.deleteBinding.mockResolvedValue(undefined)
  })

  it('loads sanitized configuration and keeps the secret blank when saving', async () => {
    const wrapper = mountModal()
    await flushPromises()
    expect(api.getBinding).toHaveBeenCalledWith(42)
    expect(wrapper.get<HTMLInputElement>('#openai-vm-secret').element.value).toBe('')
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith({ enabled: true, base_url: config.base_url, proxy_url: config.proxy_url })
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.saved')
    wrapper.unmount()
  })

  it('sends a replacement secret only on save and clears it after success', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('#openai-vm-secret').setValue('replacement-secret')
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-test"]').element.disabled).toBe(true)
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-create"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="vm-config-form"]').trigger('submit')
    await flushPromises()
    expect(api.saveConfig).toHaveBeenCalledWith(expect.objectContaining({ secret: 'replacement-secret' }))
    expect(wrapper.get<HTMLInputElement>('#openai-vm-secret').element.value).toBe('')
    wrapper.unmount()
  })

  it('health testing never creates a binding and rejects an unhealthy response', async () => {
    api.testConnection.mockResolvedValueOnce({ healthy: false, message: 'Bridge unavailable' })
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="vm-test"]').trigger('click')
    await flushPromises()
    expect(api.testConnection).toHaveBeenCalledTimes(1)
    expect(api.createBinding).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('Bridge unavailable')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.healthy')
    wrapper.unmount()
  })

  it('creates the selected runtime and blocks repeated actions and closing while busy', async () => {
    let resolveCreate!: (value: OpenAIVMBinding) => void
    api.createBinding.mockImplementationOnce(() => new Promise(resolve => { resolveCreate = resolve }))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('#openai-vm-runtime').setValue('kvm')
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    wrapper.findComponent(BaseDialog).vm.$emit('close')
    expect(api.createBinding).toHaveBeenCalledOnce()
    expect(api.createBinding).toHaveBeenCalledWith(42, 'kvm')
    expect(wrapper.emitted('close')).toBeUndefined()
    expect(wrapper.findComponent(BaseDialog).props('closeOnEscape')).toBe(false)
    api.getBinding.mockResolvedValue(state(binding))
    resolveCreate(binding)
    await flushPromises()
    expect(wrapper.text()).toContain('slot-42')
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.readySuccess')
    expect(wrapper.emitted('updated')).toHaveLength(1)
    wrapper.unmount()
  })

  it('retains a timeout error and server-side binding without claiming success', async () => {
    const wrapper = mountModal()
    await flushPromises()
    api.createBinding.mockRejectedValueOnce(new Error('request timeout'))
    api.getBinding.mockResolvedValue(state({ ...binding, status: 'error', last_error: 'slot failed' }))
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('request timeout')
    expect(wrapper.text()).toContain('slot failed')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.readySuccess')
    expect(wrapper.find('[data-testid="vm-retry"]').exists()).toBe(true)
    wrapper.unmount()
  })

  it('requires delete confirmation and preserves failed cleanup for retry', async () => {
    api.getBinding.mockResolvedValue(state(binding))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="vm-delete"]').trigger('click')
    expect(api.deleteBinding).not.toHaveBeenCalled()
    api.deleteBinding.mockRejectedValueOnce(new Error('cleanup timeout'))
    api.getBinding.mockResolvedValue(state({ ...binding, status: 'delete_failed', last_error: 'destroy failed' }))
    await wrapper.get('[data-testid="vm-delete-confirm"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('cleanup timeout')
    expect(wrapper.text()).toContain('destroy failed')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.unboundSuccess')
    expect(wrapper.get('[data-testid="vm-retry"]').text()).toContain('admin.accounts.openaiVm.retryCleanup')
    api.retryBinding.mockResolvedValueOnce(null)
    api.getBinding.mockResolvedValue(state())
    await wrapper.get('[data-testid="vm-retry"]').trigger('click')
    await flushPromises()
    expect(api.retryBinding).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.unboundSuccess')
    wrapper.unmount()
  })

  it('uses step-up authentication before retrying a sensitive action', async () => {
    const wrapper = mountModal()
    await flushPromises()
    api.createBinding.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller')
    expect(controller.visible.value).toBe(true)
    expect(api.createBinding).toHaveBeenCalledTimes(1)
    api.getBinding.mockResolvedValue(state(binding))
    controller.onVerified()
    await flushPromises()
    expect(api.createBinding).toHaveBeenCalledTimes(2)
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.readySuccess')
    wrapper.unmount()
  })

  it('does not expose creation controls when binding lookup fails', async () => {
    api.getBinding.mockRejectedValueOnce(new Error('cannot read binding'))
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.text()).toContain('cannot read binding')
    expect(wrapper.find('[data-testid="vm-create"]').exists()).toBe(false)
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.unbound')
    wrapper.unmount()
  })

  it('offers retry when a previous process left the slot provisioning', async () => {
    api.getBinding.mockResolvedValue(state({ ...binding, status: 'provisioning' }))
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get('[data-testid="vm-retry"]').text()).toContain('admin.accounts.openaiVm.retry')
    api.getBinding.mockResolvedValue(state(binding))
    await wrapper.get('[data-testid="vm-retry"]').trigger('click')
    await flushPromises()
    expect(api.retryBinding).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.readySuccess')
    wrapper.unmount()
  })

  it('does not report cleanup when creation returns without a confirmed binding', async () => {
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.createUnconfirmed')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.unboundSuccess')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.readySuccess')
    wrapper.unmount()
  })

  it('keeps errors visible during a retry and cancels step-up without claiming success', async () => {
    const wrapper = mountModal()
    await flushPromises()
    api.createBinding.mockRejectedValueOnce(new Error('first failure'))
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await flushPromises()
    api.createBinding.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await flushPromises()
    expect(wrapper.text()).toContain('first failure')
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller')
    controller.onCancel()
    await flushPromises()
    expect(wrapper.text()).toContain('first failure')
    expect(wrapper.text()).not.toContain('admin.accounts.openaiVm.readySuccess')
    expect(wrapper.findComponent(BaseDialog).props('closeOnEscape')).toBe(true)
    wrapper.unmount()
  })

  it.each([
    { platform: 'openai', type: 'oauth', parent_account_id: 9 },
    { platform: 'openai', type: 'apikey', parent_account_id: null },
    { platform: 'anthropic', type: 'oauth', parent_account_id: null }
  ])('does not allow provisioning ineligible accounts even if the API says eligible', async (override) => {
    const wrapper = mountModal({ ...account, ...override } as Account)
    await flushPromises()
    expect(wrapper.find('[data-testid="vm-create"]').exists()).toBe(false)
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.ineligible')
    wrapper.unmount()
  })
})
