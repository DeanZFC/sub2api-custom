import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import QualityOperationsPanel from '../QualityOperationsPanel.vue'
import { defaultQualitySupplier, type QualityOperationsConfig, type QualityOperationsSnapshot } from '@/api/admin/qualityOperations'
import resources from '@/i18n/locales/en/admin/resources'
import type { AccountListItem } from '@/types'
const api = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), saveSupplier: vi.fn(), deleteSupplier: vi.fn() }))
vi.mock('@/api/admin/qualityOperations', async original => ({ ...await original<object>(), qualityOperationsAPI: api }))
const config: QualityOperationsConfig = { version: 1, budget_enabled: false, daily_requests: 100, daily_tokens: 100000, account_daily_requests: 10, account_daily_tokens: 10000, token_reservation: 1024, dynamic_enabled: false, healthy_rounds: 3, healthy_base_seconds: 900, healthy_max_seconds: 3600 }
const snapshot = (): QualityOperationsSnapshot => ({ config: { ...config }, day: '2026-10-22', resets_at: '2026-10-23T00:00:00Z', usage: [{ account_id: 0, requests: 4, tokens: 4096, unknown_requests: 4 }], suppliers: [] })
const mountPanel = (accounts: AccountListItem[] = []) => mount(QualityOperationsPanel, { props: { visible: true, accounts }, global: { plugins: [createI18n({ legacy: false, locale: 'en', missingWarn: false, fallbackWarn: false, messages: { en: { admin: resources } } })], stubs: { PassiveQualityRuntimePanel: true, BaseDialog: { props: ['show'], template: '<div v-if="show"><slot/><slot name="footer"/></div>' } } } })
beforeEach(() => { vi.clearAllMocks(); api.get.mockResolvedValue(snapshot()); api.save.mockImplementation(async c => ({ ...c, version: 2 })) })
describe('quality operations', () => {
  it('validates and saves the visual reviewer without mutating the loaded configuration', async () => {
    const data = snapshot(); api.get.mockResolvedValue(data)
    const accounts = [{ id: 8, name: 'Vision judge', platform: 'openai' }, { id: 9, name: 'Other', platform: 'anthropic' }] as AccountListItem[]
    const wrapper = mountPanel(accounts); await flushPromises()
    expect(wrapper.find('[data-pelican-review-fields]').exists()).toBe(false)
    await wrapper.get('[data-pelican-review-global-enabled]').setValue(true)
    expect(wrapper.get('[data-save-operations]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-pelican-review-account]').findAll('option')).toHaveLength(2)
    await wrapper.get('[data-pelican-review-account]').setValue(8)
    await wrapper.get('[data-pelican-review-model]').setValue('gpt-vision')
    await wrapper.get('[data-pelican-review-budget]').setValue(321)
    expect(wrapper.get('[data-save-operations]').attributes('disabled')).toBeUndefined()
    expect(data.config.pelican_review).toBeUndefined()
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ pelican_review: {
      enabled: true, judge_account_id: 8, judge_model: 'gpt-vision', daily_requests: 321,
    } }))
    wrapper.unmount()
  })

  it('preserves dirty visual reviewer settings during refresh without sharing nested API state', async () => {
    const data = snapshot()
    data.config.pelican_review = { enabled: true, judge_account_id: 8, judge_model: 'vision-a', daily_requests: 200 }
    api.get.mockResolvedValue(data)
    const wrapper = mountPanel([{ id: 8, name: 'Vision judge', platform: 'openai' }] as AccountListItem[]); await flushPromises()
    await wrapper.get('[data-pelican-review-model]').setValue('vision-b')
    expect(data.config.pelican_review.judge_model).toBe('vision-a')
    await wrapper.vm.refresh(); await flushPromises()
    expect(wrapper.get<HTMLInputElement>('[data-pelican-review-model]').element.value).toBe('vision-b')
    wrapper.unmount()
  })

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
    await wrapper.get('input[type="checkbox"]').setValue(true)
    await wrapper.get('input[type="number"]').setValue(123)
    const next = snapshot(); next.config.daily_requests = 500; api.get.mockResolvedValue(next)
    await wrapper.vm.refresh(); await flushPromises()
    expect((wrapper.get('input[type="number"]').element as HTMLInputElement).value).toBe('123')
    wrapper.unmount()
  })
  it('hides disabled sections without discarding their configured values', async () => {
    const wrapper = mountPanel(); await flushPromises()
    expect(wrapper.get('[data-budget-fields]').isVisible()).toBe(false)
    expect(wrapper.get('[data-budget-fields]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[data-frequency-fields]').isVisible()).toBe(false)
    const checkboxes = wrapper.findAll('form > label input[type="checkbox"]')
    await checkboxes[0].setValue(true)
    expect(wrapper.get('[data-budget-fields]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-budget-fields]').attributes('disabled')).toBeUndefined()
    await checkboxes[1].setValue(true)
    expect(wrapper.get('[data-frequency-fields]').attributes('style') || '').not.toContain('display: none')
    await checkboxes[0].setValue(false)
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ daily_requests: 100, account_daily_requests: 10, budget_enabled: false, dynamic_enabled: true }))
    wrapper.unmount()
  })

  it('reveals invalid hidden settings and blocks invalid submissions', async () => {
    const data = snapshot(); data.config.token_reservation = 1; data.config.healthy_max_seconds = 60
    api.get.mockResolvedValue(data)
    const wrapper = mountPanel(); await flushPromises()
    expect(wrapper.get('[data-budget-fields]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-budget-fields] details').attributes('open')).toBeDefined()
    expect(wrapper.get('[data-budget-fields]').attributes('disabled')).toBeUndefined()
    expect(wrapper.get('[data-frequency-fields]').attributes('style') || '').not.toContain('display: none')
    expect(wrapper.get('[data-save-operations]').attributes('disabled')).toBeDefined()
    expect(wrapper.get('[role="alert"]').text()).toContain('1024')
    await wrapper.get('form').trigger('submit')
    expect(api.save).not.toHaveBeenCalled()
    wrapper.unmount()
  })

})
