import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { enableAutoUnmount, flushPromises, mount } from '@vue/test-utils'
import { ref } from 'vue'
import OvhIPv6StorageDialog from '../OvhIPv6StorageDialog.vue'
import type { OvhIPv6StorageStats } from '@/api/admin/ovhIPv6'

const { getStorage } = vi.hoisted(() => ({ getStorage: vi.fn() }))
vi.mock('@/api/admin/ovhIPv6', () => ({ ovhIPv6API: { getStorage } }))
vi.mock('vue-i18n', async () => ({ ...await vi.importActual<typeof import('vue-i18n')>('vue-i18n'), useI18n: () => ({ locale: ref('zh-CN'), t: (key: string, args?: Record<string, unknown>) => args ? `${key} ${JSON.stringify(args)}` : key }) }))
enableAutoUnmount(afterEach)

function response(): OvhIPv6StorageStats {
  return {
    sampled_at: '2026-11-01T10:00:00Z', issued: '18446744073709551614', prefix: '2001:db8::/64', control_available: true,
    files: { configured: true, available: true, items: [
      { name: 'counter.json', bytes: 50, disk_bytes: 4096 },
      { name: 'counter.guard.json', bytes: 50, disk_bytes: 4096 },
      { name: 'leases.json', bytes: 220, disk_bytes: 4096 }
    ], counter_bytes: 100, counter_disk_bytes: 8192, total_bytes: 320, total_disk_bytes: 12288 },
    database: { available: true, rows: 3, reserved: 1, bound: 1, retiring: 1, table_bytes: 8192, index_bytes: 16384, total_bytes: 24576 }
  }
}
function open(show = true) {
  return mount(OvhIPv6StorageDialog, { props: { show }, global: { stubs: { BaseDialog: { template: '<div><slot/><slot name="footer"/></div>' }, Icon: true } } })
}
beforeEach(() => { vi.clearAllMocks(); getStorage.mockResolvedValue(response()) })

describe('OVH IPv6 storage observations', () => {
  it('preserves a full-width issued number and reports measured storage independently', async () => {
    const wrapper = open()
    await flushPromises()
    expect(wrapper.get('[data-testid="ipv6-issued-range"]').text()).toContain('18446744073709551614')
    expect(wrapper.get('[data-testid="ipv6-counter-bytes"]').text()).toBe('100 B')
    expect(wrapper.get('[data-testid="ipv6-database-bytes"]').text()).toContain('24,576 B')
    expect(wrapper.text()).toContain('counter.guard.json')
    expect(wrapper.text()).toContain('ovhIPv6.storage.rangeHint')
    expect(getStorage).toHaveBeenCalledWith(expect.any(AbortSignal))
  })

  it('keeps unknown storage distinct from zero and retains the available issued boundary', async () => {
    const data = response()
    data.control_available = false; data.control_error = 'allocator temporarily unavailable'
    data.files.available = false; data.files.error = 'state mount unavailable'; data.files.counter_bytes = null; data.files.counter_disk_bytes = null
    data.database.available = false; data.database.error = 'database unavailable'; data.database.total_bytes = null; data.database.rows = null
    getStorage.mockResolvedValue(data)
    const wrapper = open(); await flushPromises()
    expect(wrapper.get('[data-testid="ipv6-counter-bytes"]').text()).toBe('ovhIPv6.storage.unknown')
    expect(wrapper.get('[data-testid="ipv6-database-bytes"]').text()).toBe('ovhIPv6.storage.unknown')
    expect(wrapper.get('[data-testid="ipv6-issued-range"]').text()).toContain('18446744073709551614')
    expect(wrapper.text()).toContain('state mount unavailable')
    expect(wrapper.text()).toContain('database unavailable')
    expect(wrapper.text()).toContain('allocator temporarily unavailable')
  })

  it('loads only on open and manual refresh, including retry after failure', async () => {
    getStorage.mockRejectedValueOnce(new Error('offline')).mockResolvedValueOnce({ ...response(), issued: null })
    const wrapper = open(false)
    expect(getStorage).not.toHaveBeenCalled()
    await wrapper.setProps({ show: true }); await flushPromises()
    expect(wrapper.get('[role="alert"]').text()).toBe('offline')
    await wrapper.get('[data-testid="refresh-ipv6-storage"]').trigger('click'); await flushPromises()
    expect(getStorage).toHaveBeenCalledTimes(2)
    expect(wrapper.find('[role="alert"]').exists()).toBe(false)
    expect(wrapper.get('[data-testid="ipv6-issued-range"]').text()).toBe('ovhIPv6.storage.issuedUnknown')
  })

  it('cancels closed requests and rejects stale results after reopening', async () => {
    let resolveFirst!: (data: OvhIPv6StorageStats) => void
    getStorage.mockImplementationOnce(() => new Promise(resolve => { resolveFirst = resolve }))
    const wrapper = open()
    const firstSignal = getStorage.mock.calls[0]![0] as AbortSignal
    await wrapper.setProps({ show: false })
    expect(firstSignal.aborted).toBe(true)
    await wrapper.setProps({ show: true }); await flushPromises()
    resolveFirst({ ...response(), issued: '0' }); await flushPromises()
    expect(wrapper.get('[data-testid="ipv6-issued-range"]').text()).toContain('18446744073709551614')
    expect(getStorage).toHaveBeenCalledTimes(2)
  })
})
