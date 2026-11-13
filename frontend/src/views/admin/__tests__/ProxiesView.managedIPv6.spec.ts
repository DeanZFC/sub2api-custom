import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, shallowMount } from '@vue/test-utils'
import ProxiesView from '../ProxiesView.vue'

const { list, getAllWithCount, get, save, batchDelete, allocate, deleteProxy } = vi.hoisted(() => ({
  list: vi.fn(), getAllWithCount: vi.fn(), get: vi.fn(), save: vi.fn(), batchDelete: vi.fn(), allocate: vi.fn(), deleteProxy: vi.fn()
}))
vi.mock('@/api/admin', () => ({ adminAPI: { proxies: { list, getAllWithCount, batchDelete, delete: deleteProxy } } }))
vi.mock('@/api/admin/ovhIPv6', () => ({ ovhIPv6API: { allocate } }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showError: vi.fn(), showSuccess: vi.fn(), showInfo: vi.fn() }) }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ t: (key: string) => key }) }))

enableAutoUnmount(afterEach)

const managed = {
  id: 1, name: 'assigned-ipv6', managed_ipv6: '2001:db8:1234::10',
  host: 'egress.internal', port: 1080, protocol: 'socks5',
  username: 'internal-user', password: 'internal-secret', account_count: 1
}
const manual = { id: 2, name: 'imported-proxy', host: 'proxy.example', port: 8080, protocol: 'http', account_count: 0 }

const mountView = () => shallowMount(ProxiesView, { global: { stubs: {
  AppLayout: { template: '<div><slot /></div>' },
  TablePageLayout: { template: '<div><slot name="filters" /><slot name="table" /><slot name="pagination" /></div>' },
  DataTable: { props: ['data'], template: `<div><div v-for="row in data" :key="row.id" :data-row="row.id">
    <slot name="cell-select" :row="row" /><slot name="cell-name" :row="row" :value="row.name" />
    <slot name="cell-address" :row="row" /><slot name="cell-auth" :row="row" />
    <slot name="cell-account_count" :row="row" :value="row.account_count" /><slot name="cell-actions" :row="row" />
  </div></div>` },
  Pagination: { emits: ['update:page'], template: '<button data-test="next-page" @click="$emit(\'update:page\', 2)">next</button>' },
  BaseDialog: { props: ['show', 'title'], template: '<div v-if="show" :data-title="title"><slot /><slot name="footer" /></div>' },
  ConfirmDialog: { props: ['show'], emits: ['confirm'], template: '<button v-if="show" data-test="confirm-delete" @click="$emit(\'confirm\')">confirm</button>' }
} } })

beforeEach(() => {
  vi.clearAllMocks()
  list.mockResolvedValue({ items: [managed, manual], total: 2, pages: 1 })
  getAllWithCount.mockResolvedValue([managed, manual])
  get.mockResolvedValue([{ proxy_id: 2, max_accounts: 3, account_count: 0 }])
  save.mockResolvedValue([])
  batchDelete.mockResolvedValue({ deleted_ids: [2], skipped: [] })
})

describe('managed IPv6 proxy entries', () => {
  it('shows the egress address and fixed exclusive binding without credentials or mutable actions', async () => {
    const wrapper = mountView()
    await flushPromises()
    const row = wrapper.get('[data-row="1"]')
    expect(row.text()).toContain('2001:db8:1234::10')
    expect(row.text()).toContain('ovhIPv6.source')
    for (const hidden of ['egress.internal', 'internal-user', 'internal-secret', 'common.edit', 'common.delete']) {
      expect(row.text()).not.toContain(hidden)
    }
    expect(row.find('[title="admin.proxies.copyProxyUrl"]').exists()).toBe(false)
    expect(row.text()).toContain('admin.proxies.testConnection')
    expect(wrapper.get('[data-row="2"]').text()).toContain('common.edit')
    expect(wrapper.get('[data-row="2"]').text()).toContain('common.delete')
  })

  it('excludes managed selections from bulk deletion', async () => {
    const wrapper = mountView()
    await flushPromises()
    for (const checkbox of wrapper.findAll('input[type="checkbox"]')) await checkbox.setValue(true)
    await wrapper.get('button[title="admin.proxies.batchDeleteAction"]').trigger('click')
    await wrapper.get('[data-test="confirm-delete"]').trigger('click')
    await flushPromises()
    expect(batchDelete).toHaveBeenCalledWith([2])
  })
})


describe('single reserved IPv6', () => {
  it('allocates exactly one address and refreshes the list', async () => {
    let finish!: (value: object) => void
    allocate.mockImplementation(() => new Promise(resolve => { finish = resolve }))
    const wrapper = mountView(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('admin.proxies.createProxy'))!.trigger('click')
    await wrapper.get('[data-testid="add-ovh-ipv6-tab"]').trigger('click')
    await wrapper.get('#allocate-ovh-ipv6-form input').setValue('new reservation')
    await wrapper.get('#allocate-ovh-ipv6-form').trigger('submit')
    expect(allocate).toHaveBeenCalledWith('new reservation')
    expect(wrapper.get('[data-testid="allocate-ovh-ipv6"]').attributes('disabled')).toBeDefined()
    finish({ id: 3, managed_ipv6: '2001:db8::3', managed_ipv6_state: 'reserved' }); await flushPromises()
    expect(allocate).toHaveBeenCalledTimes(1)
    expect(list).toHaveBeenCalledTimes(2)
    expect(wrapper.find('#allocate-ovh-ipv6-form').exists()).toBe(false)
  })

  it('keeps allocation errors visible and lets the user retry without pretending it succeeded', async () => {
    allocate.mockRejectedValue(new Error('egress service unavailable'))
    const wrapper = mountView(); await flushPromises()
    await wrapper.findAll('button').find(button => button.text().includes('admin.proxies.createProxy'))!.trigger('click')
    await wrapper.get('[data-testid="add-ovh-ipv6-tab"]').trigger('click')
    await wrapper.get('#allocate-ovh-ipv6-form').trigger('submit'); await flushPromises()
    expect(wrapper.get('#allocate-ovh-ipv6-form [role="alert"]').text()).toContain('egress service unavailable')
    expect(wrapper.get('[data-testid="allocate-ovh-ipv6"]').attributes('disabled')).toBeUndefined()
    expect(list).toHaveBeenCalledTimes(1)
  })

  it('allows unused reservations to be retired with the normal delete action', async () => {
    list.mockResolvedValue({ items: [{ ...managed, account_count: 0, managed_ipv6_state: 'reserved' }], total: 1, pages: 1 })
    deleteProxy.mockResolvedValue({})
    const wrapper = mountView(); await flushPromises()
    const row = wrapper.get('[data-row="1"]')
    expect(row.text()).toContain('ovhIPv6.reserved')
    expect(row.text()).not.toContain('common.edit')
    await row.findAll('button').find(button => button.text() === 'common.delete')!.trigger('click')
    await wrapper.get('[data-test="confirm-delete"]').trigger('click'); await flushPromises()
    expect(deleteProxy).toHaveBeenCalledWith(1)
  })
})
