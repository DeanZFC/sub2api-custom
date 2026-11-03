import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, mount } from '@vue/test-utils'
import { defineComponent } from 'vue'
import CodexTicketSettings from '../settings/CodexTicketSettings.vue'
import ExcelBPSImageSettings from '../settings/ExcelBPSImageSettings.vue'

const { getSettings, updateSettings, getAll, showSuccess } = vi.hoisted(() => ({
  getSettings: vi.fn(), updateSettings: vi.fn(), getAll: vi.fn(), showSuccess: vi.fn()
}))
vi.mock('@/api/admin/settings', () => ({ getSettings, updateSettings }))
vi.mock('@/api/admin/groups', () => ({ getAll }))
vi.mock('@/stores/app', () => ({ useAppStore: () => ({ showSuccess }) }))
vi.mock('vue-i18n', async () => ({ ...(await vi.importActual<typeof import('vue-i18n')>('vue-i18n')), useI18n: () => ({ locale: { value: 'zh-CN' }, t: (key: string) => key }) }))

const Toggle = defineComponent({
  props: { modelValue: Boolean }, emits: ['update:modelValue'],
  template: '<input type="checkbox" :checked="modelValue" @change="$emit(\'update:modelValue\', $event.target.checked)" />'
})
const options = { global: { stubs: { ModelWhitelistSelector: true, MihomoProxySelector: true, RouterLink: true, Toggle } } }
const wrappers: { unmount(): void }[] = []
afterEach(() => { for (const wrapper of wrappers.splice(0)) wrapper.unmount() })
beforeEach(() => {
  vi.clearAllMocks()
  getSettings.mockResolvedValue({})
  updateSettings.mockResolvedValue({})
  getAll.mockResolvedValue([])
})

describe('independently saved relay settings', () => {
  it('preserves a selected harvest scope if group loading fails and sends only ticket settings', async () => {
    getAll.mockRejectedValue(new Error('offline'))
    getSettings.mockResolvedValue({
      site_name: 'preserve this setting',
      openai_codex_ticket_enabled: true,
      openai_codex_ticket_harvest_scope: { mode: 'selected', group_ids: [42], account_policy: 'schedulable_only' },
      openai_codex_ticket_harvest_proxy_configured: true
    })
    const wrapper = mount(CodexTicketSettings, options); wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.text()).toContain('#42')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledOnce()
    const body = updateSettings.mock.calls[0]![0]
    expect(body.openai_codex_ticket_harvest_scope).toEqual({ mode: 'selected', group_ids: [42], account_policy: 'schedulable_only' })
    expect(body.openai_codex_ticket_use_saved_static_proxy).toBe(true)
    expect(body).not.toHaveProperty('site_name')
  })

  it('offers only OpenAI groups for a new ticket harvesting scope', async () => {
    getAll.mockResolvedValue([
      { id: 1, name: 'OpenAI scope', platform: 'openai' },
      { id: 2, name: 'Composite scope', platform: 'composite' },
      { id: 3, name: 'Claude scope', platform: 'anthropic' },
    ])
    const wrapper = mount(CodexTicketSettings, options); wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('#codex-ticket-harvest-scope').setValue('selected')
    expect(wrapper.text()).toContain('OpenAI scope')
    expect(wrapper.text()).not.toContain('Composite scope')
    expect(wrapper.text()).not.toContain('Claude scope')
    await wrapper.get('input[type="checkbox"][value="1"]').setValue(true)
    await wrapper.get('form').trigger('submit')
    expect(updateSettings.mock.calls[0]![0].openai_codex_ticket_harvest_scope).toMatchObject({ mode: 'selected', group_ids: [1] })
  })

  it('does not broaden an empty selected scope or select an unready Mihomo kernel', async () => {
    const wrapper = mount(CodexTicketSettings, options); wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('#codex-ticket-harvest-scope').setValue('selected')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings.mock.calls[0]![0].openai_codex_ticket_harvest_scope).toMatchObject({ mode: 'selected', group_ids: [] })
    updateSettings.mockClear()
    await wrapper.get('#codex-ticket-proxy-mode').setValue('mihomo')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    expect(wrapper.get('[role="alert"]').text()).toContain('内核')
  })

  it('keeps saving disabled when initial settings cannot be read', async () => {
    getSettings.mockRejectedValue(new Error('read failed'))
    const wrapper = mount(ExcelBPSImageSettings, options); wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.get('button[type="submit"]').attributes('disabled')).toBeDefined()
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
  })

  it('requires a credential-free HTTPS origin for relay mode and validates resource budgets', async () => {
    getSettings.mockResolvedValue({ excel_bps_image_relay_enabled: true, excel_bps_image_base_url: 'https://relay.example' })
    const wrapper = mount(ExcelBPSImageSettings, options); wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('#excel-bps-image-base-url').setValue('https://user:pass@relay.example')
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    await wrapper.get('#excel-bps-image-base-url').setValue('https://relay.example')
    await wrapper.get('#excel-bps-image-body-limit').setValue(128)
    await wrapper.get('#excel-bps-image-budget').setValue(512)
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).not.toHaveBeenCalled()
    await wrapper.get('#excel-bps-image-budget').setValue(1024)
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledWith(expect.objectContaining({ excel_bps_image_base_url: 'https://relay.example', excel_bps_image_budget_mib: 1024 }))
  })

  it('allows native image mode without a relay URL and keeps image-only payloads', async () => {
    getSettings.mockResolvedValue({ excel_bps_image_relay_enabled: true, excel_bps_image_mode: 'native', site_name: 'keep' })
    const wrapper = mount(ExcelBPSImageSettings, options); wrappers.push(wrapper)
    await flushPromises()
    await wrapper.get('form').trigger('submit')
    expect(updateSettings).toHaveBeenCalledOnce()
    expect(updateSettings.mock.calls[0]![0]).toMatchObject({ excel_bps_image_mode: 'native', excel_bps_image_base_url: '' })
    expect(updateSettings.mock.calls[0]![0]).not.toHaveProperty('site_name')
  })
})
