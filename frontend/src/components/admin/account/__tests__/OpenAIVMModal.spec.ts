import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createMemoryHistory, createRouter } from 'vue-router'
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

const account = { id: 42, name: 'OpenAI parent', platform: 'openai', type: 'oauth', parent_account_id: null } as Account
const binding: OpenAIVMBinding = { account_id: 42, slot_id: 'slot-42', status: 'ready', last_error: null, created_at: '2026-10-24T00:00:00Z', updated_at: '2026-10-24T00:00:00Z' }
const state = (value: OpenAIVMBinding | null = null, configured = true) => ({ eligible: true, configured, binding: value })

function makeRouter() {
  return createRouter({
    history: createMemoryHistory(),
    routes: [{ path: '/', component: { template: '<div />' } }, { path: '/admin/settings', component: { template: '<div />' } }]
  })
}

function mountModal(value = account, router = makeRouter()) {
  return mount(OpenAIVMModal, {
    props: { show: true, account: value },
    global: { plugins: [router], stubs: {
      BaseDialog: { props: ['show', 'showCloseButton', 'closeOnEscape'], emits: ['close'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
      TotpStepUpDialog: { props: ['controller'], template: '<div />' },
      Icon: true
    } }
  })
}

describe('OpenAI dedicated slot modal', () => {
  beforeEach(() => {
    Object.values(api).forEach(fn => fn.mockReset())
    api.getBinding.mockResolvedValue(state())
    api.createBinding.mockResolvedValue(binding)
    api.retryBinding.mockResolvedValue(binding)
    api.deleteBinding.mockResolvedValue(undefined)
  })

  it('only loads account binding state and does not expose global configuration', async () => {
    const wrapper = mountModal()
    await flushPromises()
    expect(api.getBinding).toHaveBeenCalledWith(42)
    expect(api.getConfig).not.toHaveBeenCalled()
    expect(api.saveConfig).not.toHaveBeenCalled()
    expect(api.testConnection).not.toHaveBeenCalled()
    expect(wrapper.find('[data-testid="vm-config-form"]').exists()).toBe(false)
    expect(wrapper.find('#openai-vm-secret').exists()).toBe(false)
    expect(wrapper.find('#openai-vm-base-url').exists()).toBe(false)
    expect(wrapper.find('#openai-vm-proxy-url').exists()).toBe(false)
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-create"]').element.disabled).toBe(false)
    expect(wrapper.find('[data-testid="vm-settings-link"]').exists()).toBe(false)
    wrapper.unmount()
  })

  it('disables creation until configured and links to the global settings tab', async () => {
    api.getBinding.mockResolvedValue(state(null, false))
    const router = makeRouter()
    const wrapper = mountModal(account, router)
    await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-create"]').element.disabled).toBe(true)
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    expect(api.createBinding).not.toHaveBeenCalled()
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.notConfigured')
    expect(wrapper.get('[data-testid="vm-settings-link"]').attributes('href')).toBe('/admin/settings?tab=openai-vm')
    await wrapper.get('[data-testid="vm-settings-link"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/admin/settings?tab=openai-vm')
    expect(wrapper.emitted('close')).toHaveLength(1)
    wrapper.unmount()
  })

  it('blocks settings navigation and closing while binding state is refreshing', async () => {
    api.getBinding.mockResolvedValue(state(null, false))
    const router = makeRouter()
    const wrapper = mountModal(account, router)
    await flushPromises()
    let resolveRefresh!: (value: ReturnType<typeof state>) => void
    api.getBinding.mockImplementationOnce(() => new Promise(resolve => { resolveRefresh = resolve }))
    await wrapper.get('[data-testid="vm-refresh"]').trigger('click')
    expect(wrapper.get('[data-testid="vm-settings-link"]').attributes('href')).toBeUndefined()
    expect(wrapper.get('[data-testid="vm-settings-link"]').attributes('aria-disabled')).toBe('true')
    await wrapper.get('[data-testid="vm-settings-link"]').trigger('click')
    wrapper.findComponent(BaseDialog).vm.$emit('close')
    expect(router.currentRoute.value.fullPath).toBe('/')
    expect(wrapper.emitted('close')).toBeUndefined()
    resolveRefresh(state(null, false))
    await flushPromises()
    expect(wrapper.get('[data-testid="vm-settings-link"]').attributes('aria-disabled')).toBe('false')
    wrapper.unmount()
  })

  it('keeps the modal open when settings navigation is aborted', async () => {
    api.getBinding.mockResolvedValue(state(null, false))
    const router = makeRouter()
    const wrapper = mountModal(account, router)
    await flushPromises()
    router.beforeEach(to => to.path === '/admin/settings' ? false : true)
    await wrapper.get('[data-testid="vm-settings-link"]').trigger('click')
    await flushPromises()
    expect(router.currentRoute.value.fullPath).toBe('/')
    expect(wrapper.emitted('close')).toBeUndefined()
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

  it('requires global configuration for provisioning retries but permits cleanup retries', async () => {
    api.getBinding.mockResolvedValue(state({ ...binding, status: 'error' }, false))
    const wrapper = mountModal()
    await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-retry"]').element.disabled).toBe(true)
    api.getBinding.mockResolvedValue(state({ ...binding, status: 'delete_failed' }, false))
    await wrapper.get('[data-testid="vm-refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.get<HTMLButtonElement>('[data-testid="vm-retry"]').element.disabled).toBe(false)
    api.retryBinding.mockResolvedValueOnce(null)
    api.getBinding.mockResolvedValue(state(null, false))
    await wrapper.get('[data-testid="vm-retry"]').trigger('click')
    await flushPromises()
    expect(api.retryBinding).toHaveBeenCalledWith(42)
    expect(wrapper.text()).toContain('admin.accounts.openaiVm.unboundSuccess')
    wrapper.unmount()
  })

  it('ignores binding results from a previous account', async () => {
    let resolveInitial!: (value: ReturnType<typeof state>) => void
    api.getBinding.mockImplementationOnce(() => new Promise(resolve => { resolveInitial = resolve }))
    const wrapper = mountModal()
    await wrapper.setProps({ account: { ...account, id: 43 } })
    await flushPromises()
    resolveInitial(state(binding))
    await flushPromises()
    expect(wrapper.text()).not.toContain('slot-42')
    expect(api.getBinding).toHaveBeenLastCalledWith(43)
    wrapper.unmount()
  })

  it('does not prompt for or retry step-up when the account changes during a request', async () => {
    let rejectCreate!: (cause: unknown) => void
    api.createBinding.mockImplementationOnce(() => new Promise((_resolve, reject) => { rejectCreate = reject }))
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await wrapper.setProps({ account: { ...account, id: 43 } })
    rejectCreate({ code: 'STEP_UP_REQUIRED' })
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller')
    expect(controller.visible.value).toBe(false)
    expect(api.createBinding).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('updated')).toBeUndefined()
    wrapper.unmount()
  })

  it('cancels pending step-up on unmount without retrying the mutation', async () => {
    api.createBinding.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' })
    const wrapper = mountModal()
    await flushPromises()
    await wrapper.get('[data-testid="vm-create"]').trigger('click')
    await flushPromises()
    const controller = wrapper.findComponent(TotpStepUpDialog).props('controller')
    expect(controller.visible.value).toBe(true)
    wrapper.unmount()
    controller.onVerified()
    await flushPromises()
    expect(api.createBinding).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('updated')).toBeUndefined()
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
