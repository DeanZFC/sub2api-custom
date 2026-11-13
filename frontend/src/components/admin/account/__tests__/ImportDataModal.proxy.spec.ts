import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import ImportDataModal from '../ImportDataModal.vue'

const { importData, showError } = vi.hoisted(() => ({ importData: vi.fn(), showError: vi.fn() }))
vi.mock('@/api/admin', () => ({ adminAPI: { accounts: { importData }, proxies: { getAllWithCount: vi.fn().mockResolvedValue([]) } } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError, showSuccess: vi.fn(), showWarning: vi.fn() }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key }) }))
enableAutoUnmount(afterEach)
beforeEach(() => {
  vi.clearAllMocks()
  importData.mockResolvedValue({ account_created: 2, account_failed: 0, proxy_created: 0, proxy_reused: 0, proxy_failed: 0, errors: [] })
})

async function open() {
  const wrapper = mount(ImportDataModal, { props: { show: true }, global: { stubs: {
    BaseDialog: { template: '<div><slot /><slot name="footer" /></div>' }, Icon: true
  } } })
  const file = new File([JSON.stringify({ proxies: [], accounts: [{ name: 'one', proxy_key: 'file-proxy' }, { name: 'two' }] })], 'accounts.json', { type: 'application/json' })
  Object.defineProperty(file, 'text', { value: async () => JSON.stringify({ proxies: [], accounts: [{ name: 'one', proxy_key: 'file-proxy' }, { name: 'two' }] }) })
  Object.defineProperty(wrapper.get('input[type="file"]').element, 'files', { value: [file] })
  await wrapper.get('input[type="file"]').trigger('change')
  return wrapper
}

describe('JSON account import proxy selection', () => {
  it('keeps file settings by default', async () => {
    const wrapper = await open()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(importData).toHaveBeenCalledTimes(1)
    expect(importData.mock.calls[0]![0]).not.toHaveProperty('proxy_mode')
    expect(importData.mock.calls[0]![0].data.accounts[0].proxy_key).toBe('file-proxy')
  })

  it('passes explicit OVH allocation for the whole import without inventing a numeric proxy ID', async () => {
    const wrapper = await open()
    await wrapper.get('[data-testid="import-override-proxy"]').setValue(true)
    await wrapper.get('[data-testid="account-proxy-mode"]').setValue('ovh_ipv6')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(importData.mock.calls[0]![0]).toMatchObject({ proxy_mode: 'ovh_ipv6', proxy_id: null })
  })

  it('can explicitly override file settings with direct connection', async () => {
    const wrapper = await open()
    await wrapper.get('[data-testid="import-override-proxy"]').setValue(true)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(importData.mock.calls[0]![0]).toMatchObject({ proxy_mode: 'direct', proxy_id: null })
  })
})
