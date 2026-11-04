import { beforeEach, describe, expect, it, vi } from 'vitest'
import { mount, flushPromises } from '@vue/test-utils'
import { ref } from 'vue'
import ExcelBPSProtocolSettings from '../ExcelBPSProtocolSettings.vue'
const mocks = vi.hoisted(() => ({ get: vi.fn(), update: vi.fn(), refresh: vi.fn(), success: vi.fn() }))
vi.mock('@/api/admin/settings', () => ({ getSettings: mocks.get, updateSettings: mocks.update }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ fetchPublicSettings: mocks.refresh, showSuccess: mocks.success }) }))
vi.mock('vue-i18n', () => ({ useI18n: () => ({ t: (key: string) => key, locale: ref('en') }) }))
beforeEach(() => { vi.clearAllMocks(); mocks.get.mockResolvedValue({ excel_bps_enabled: true }); mocks.update.mockResolvedValue({}) })
describe('Global BPS switch', () => {
  it('persists only the BPS switch and refreshes public settings', async () => {
    const wrapper = mount(ExcelBPSProtocolSettings)
    await flushPromises()
    await wrapper.get('[role="switch"]').trigger('click')
    await wrapper.get('button.btn-primary').trigger('click')
    await flushPromises()
    expect(mocks.update).toHaveBeenCalledWith({ excel_bps_enabled: false })
    expect(mocks.refresh).toHaveBeenCalledWith(true)
    wrapper.unmount()
  })
  it('prevents saving an unknown value when loading fails', async () => {
    mocks.get.mockRejectedValueOnce(new Error('unavailable'))
    const wrapper = mount(ExcelBPSProtocolSettings)
    await flushPromises()
    expect(wrapper.get('button.btn-primary').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('Could not load')
    expect(mocks.update).not.toHaveBeenCalled()
    wrapper.unmount()
  })
})
