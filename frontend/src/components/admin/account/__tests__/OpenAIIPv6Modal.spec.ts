import { flushPromises, mount } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import OpenAIIPv6Modal from '../OpenAIIPv6Modal.vue'
import type { Account } from '@/types'
const api = vi.hoisted(() => ({ status: vi.fn(), bind: vi.fn(), detach: vi.fn() }))
vi.mock('@/api/admin/openaiIPv6', () => ({ openaiIPv6API: api }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))
const account = { id: 42, name: 'Test', platform: 'openai', type: 'oauth', status: 'inactive' } as Account
const binding = { account_id: 42, lease_id: 'sub2-test', address: '2001:db8::1', status: 'ready', last_error: '' }
const state = (value: typeof binding | null = null) => ({ eligible: true, configured: true, binding: value })
function mountModal(a = account) { return mount(OpenAIIPv6Modal, { props: { show: true, account: a }, global: { stubs: {
  BaseDialog: { props: ['show'], emits: ['close'], template: '<div v-if="show"><slot /><slot name="footer" /></div>' },
  TotpStepUpDialog: { props: ['controller'], template: '<div />' }
} } }) }
describe('fixed IPv6 account dialog', () => {
 beforeEach(() => { Object.values(api).forEach(fn => fn.mockReset()); api.status.mockResolvedValue(state()); api.bind.mockResolvedValue(binding); api.detach.mockResolvedValue(undefined) })
 it('blocks active accounts and unconfigured service', async () => {
  const wrapper = mountModal({ ...account, status: 'active' }); await flushPromises()
  expect(wrapper.get<HTMLButtonElement>('[data-testid="ipv6-bind"]').element.disabled).toBe(true)
  expect(api.bind).not.toHaveBeenCalled(); wrapper.unmount()
  api.status.mockResolvedValue({ ...state(), configured: false }); const next = mountModal(); await flushPromises()
  expect(next.get<HTMLButtonElement>('[data-testid="ipv6-bind"]').element.disabled).toBe(true); next.unmount()
 })
 it('binds an inactive account and verifies persisted status', async () => {
  const wrapper = mountModal(); await flushPromises(); api.status.mockResolvedValue(state(binding))
  await wrapper.get('[data-testid="ipv6-bind"]').trigger('click'); await flushPromises()
  expect(api.bind).toHaveBeenCalledWith(42); expect(wrapper.get('[data-testid="ipv6-address"]').text()).toBe(binding.address)
  expect(wrapper.emitted('updated')).toHaveLength(1); wrapper.unmount()
 })
 it('requires explicit confirmation before detach', async () => {
  api.status.mockResolvedValue(state(binding)); const wrapper = mountModal(); await flushPromises()
  await wrapper.get('[data-testid="ipv6-detach"]').trigger('click'); expect(api.detach).not.toHaveBeenCalled()
  api.status.mockResolvedValue(state()); await wrapper.get('[data-testid="ipv6-confirm-detach"]').trigger('click'); await flushPromises()
  expect(api.detach).toHaveBeenCalledWith(42); expect(wrapper.find('[data-testid="ipv6-address"]').exists()).toBe(false); wrapper.unmount()
 })
 it('reloads a binding after an uncertain operation failure', async () => {
  const wrapper = mountModal(); await flushPromises(); api.bind.mockRejectedValue(new Error('timeout')); api.status.mockResolvedValue(state({ ...binding, status: 'error' }))
  await wrapper.get('[data-testid="ipv6-bind"]').trigger('click'); await flushPromises()
  expect(wrapper.get('[role="alert"]').text()).toContain('timeout'); expect(wrapper.get('[data-testid="ipv6-address"]').text()).toBe(binding.address); wrapper.unmount()
 })
})
