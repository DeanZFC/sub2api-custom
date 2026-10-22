import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import QualityOperationsPanel from '../QualityOperationsPanel.vue'
import { defaultQualitySupplier, type QualityOperationsConfig, type QualityOperationsSnapshot } from '@/api/admin/qualityOperations'
import resources from '@/i18n/locales/en/admin/resources'
const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), saveSupplier: vi.fn(), deleteSupplier: vi.fn() }))
vi.mock('@/api/admin/qualityOperations', async original => ({ ...await original<object>(), qualityOperationsAPI: api }))
const config: QualityOperationsConfig = { version: 1, budget_enabled: false, daily_requests: 100, daily_tokens: 100000, account_daily_requests: 10, account_daily_tokens: 10000, token_reservation: 1024, dynamic_enabled: false, healthy_rounds: 3, healthy_base_seconds: 900, healthy_max_seconds: 3600 }
const snapshot = (): QualityOperationsSnapshot => ({ config: { ...config }, day: '2026-10-22', resets_at: '2026-10-23T00:00:00Z', usage: [{ account_id: 0, requests: 4, tokens: 4096, unknown_requests: 4 }], suppliers: [] })
const mountPanel = () => mount(QualityOperationsPanel, { props: { visible: true, accounts: [] }, global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })], stubs: { BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' } } } })
beforeEach(() => { vi.clearAllMocks(); api.get.mockResolvedValue(snapshot()); api.save.mockImplementation(async c => ({ ...c, version: 2 })) })
describe('quality operations', () => {
  it('saves versioned limits and keeps budget changes explicit', async () => {
    const wrapper = mountPanel(); await flushPromises()
    expect(api.save).not.toHaveBeenCalled()
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ version: 1, budget_enabled: true }))
    expect(wrapper.text()).toContain('4,096')
    wrapper.unmount()
  })
  it('requires deletion confirmation and submits the supplier version', async () => {
    const data = snapshot(); const supplier = { ...defaultQualitySupplier(), id: 5, version: 4, name: 'Vendor A' }; data.suppliers = [supplier]; api.get.mockResolvedValue(data)
    const wrapper = mountPanel(); await flushPromises()
    const deleteButton = wrapper.findAll('article button').at(-1)!
    await deleteButton.trigger('click'); expect(api.deleteSupplier).not.toHaveBeenCalled()
    const buttons = wrapper.findAll('button'); await buttons.at(-1)!.trigger('click'); await flushPromises()
    expect(api.deleteSupplier).toHaveBeenCalledWith(expect.objectContaining({ id: 5, version: 4 }))
    wrapper.unmount()
  })
  it('preserves unsaved budget edits while refreshing usage', async () => {
    const wrapper = mountPanel(); await flushPromises()
    await wrapper.get('input[type="number"]').setValue(123)
    const next = snapshot(); next.config.daily_requests = 500; api.get.mockResolvedValue(next)
    await wrapper.vm.refresh(); await flushPromises()
    expect((wrapper.get('input[type="number"]').element as HTMLInputElement).value).toBe('123')
    wrapper.unmount()
  })
})
