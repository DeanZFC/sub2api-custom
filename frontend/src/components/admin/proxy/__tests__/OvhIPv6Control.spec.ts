import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import OvhIPv6Control from '../OvhIPv6Control.vue'
import type { OvhIPv6Status } from '@/api/admin/ovhIPv6'

const { get, setEnabled } = vi.hoisted(() => ({ get: vi.fn(), setEnabled: vi.fn() }))
vi.mock('@/api/admin/ovhIPv6', () => ({ ovhIPv6API: { get, setEnabled } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

const status = (overrides: Partial<OvhIPv6Status> = {}): OvhIPv6Status => ({
  configured: true, available: true, enabled: false,
  prefix: '2001:db8:1234::/64', issued: '1000', bound: 2, pending_cleanup: 0,
  ...overrides
})
const mountControl = () => mount(OvhIPv6Control, { global: { stubs: { Icon: true, TotpStepUpDialog: { props: ['controller'], template: `<div v-if="controller.visible.value"><button data-test="stepup-confirm" @click="controller.onVerified()">verify</button><button data-test="stepup-cancel" @click="controller.onCancel()">cancel</button></div>` } } } })

beforeEach(() => {
  vi.clearAllMocks()
  get.mockResolvedValue(status())
})

describe('OVH IPv6 auto allocation control', () => {
  it('loads the server setting before allowing a change', async () => {
    const wrapper = mountControl()
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeUndefined()
    expect(wrapper.text()).toContain('2001:db8:1234::/64')
    expect(wrapper.text()).toContain('ovhIPv6.disabledHint')
    expect(setEnabled).not.toHaveBeenCalled()
  })

  it('waits for the save result and prevents duplicate saves', async () => {
    let finish!: (value: OvhIPv6Status) => void
    setEnabled.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountControl()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    expect(setEnabled).toHaveBeenCalledWith(true)
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    await wrapper.get('[role="switch"]').trigger('click')
    expect(setEnabled).toHaveBeenCalledTimes(1)
    expect(wrapper.emitted('changed')).toBeUndefined()
    finish(status({ enabled: true }))
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
    expect(wrapper.text()).toContain('ovhIPv6.enabledHint')
    expect(wrapper.emitted('changed')).toEqual([[status({ enabled: true })]])
  })

  it('keeps the original setting on failure and supports retry', async () => {
    setEnabled.mockRejectedValueOnce({ message: 'Allocation service unavailable' })
      .mockResolvedValueOnce(status({ enabled: true }))
    const wrapper = mountControl()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Allocation service unavailable')
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.emitted('changed')).toBeUndefined()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
  })

  it.each([
    [false, false, 'ovhIPv6.notConfigured'],
    [true, false, 'ovhIPv6.unavailable']
  ])('prevents enabling when configured=%s and available=%s', async (configured, available, message) => {
    get.mockResolvedValue(status({ configured, available }))
    const wrapper = mountControl()
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="status"]').text()).toBe(message)
    await wrapper.get('[role="switch"]').trigger('click')
    expect(setEnabled).not.toHaveBeenCalled()
  })

  it('allows disabling an enabled pool even when unavailable', async () => {
    get.mockResolvedValue(status({ enabled: true, available: false }))
    setEnabled.mockResolvedValue(status({ available: false }))
    const wrapper = mountControl()
    await flushPromises()
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeUndefined()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(setEnabled).toHaveBeenCalledWith(false)
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
  })

  it('retries loading without writing the setting', async () => {
    get.mockRejectedValueOnce(new Error('Offline')).mockResolvedValueOnce(status())
    const wrapper = mountControl()
    await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('Offline')
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeDefined()
    await wrapper.get('button[aria-label="ovhIPv6.refresh"]').trigger('click')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('[role="switch"]').attributes('disabled')).toBeUndefined()
    expect(setEnabled).not.toHaveBeenCalled()
  })
  it('requests step-up and retries the captured allocation switch exactly once', async () => {
    setEnabled.mockRejectedValueOnce({ code: 'STEP_UP_REQUIRED' }).mockResolvedValueOnce(status({ enabled: true }))
    const wrapper = mountControl()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    expect(setEnabled).toHaveBeenCalledTimes(1)
    await wrapper.get('[data-test="stepup-confirm"]').trigger('click')
    await flushPromises()
    expect(setEnabled.mock.calls).toEqual([[true], [true]])
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('true')
  })

  it('keeps the allocation switch unchanged when step-up is cancelled', async () => {
    setEnabled.mockRejectedValueOnce({ reason: 'STEP_UP_REQUIRED' })
    const wrapper = mountControl()
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await flushPromises()
    await wrapper.get('[data-test="stepup-cancel"]').trigger('click')
    await flushPromises()
    expect(setEnabled).toHaveBeenCalledTimes(1)
    expect(wrapper.get('[role="switch"]').attributes('aria-checked')).toBe('false')
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
  })

})
