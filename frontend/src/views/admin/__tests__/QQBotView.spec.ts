import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import QQBotView from '../QQBotView.vue'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

const api = vi.hoisted(() => ({ config: vi.fn(), save: vi.fn(), runtime: vi.fn(), deleteDiscoveredGroup: vi.fn(), probe: vi.fn() }))
vi.mock('@/api/qqbot', async (importOriginal) => ({ ...await importOriginal<typeof import('@/api/qqbot')>(), qqbotAPI: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('@/components/admin/QQBotBindings.vue', () => ({ default: { template: '<section />' } }))

describe('QQ bot settings', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    api.config.mockResolvedValue({ enabled: false, app_id: 'app', app_secret: '', app_secret_set: true, allowed_groups: [], base_url: 'https://example.invalid/v1', api_key: '', api_key_set: true, chat_model: 'model', chat_protocol: 'responses', image_enabled: false, image_size: '1024x1024', system_prompt: 'Helpful', max_output_tokens: 2048, history_messages: 12 })
    api.runtime.mockResolvedValue({ groups: [], jobs: [], rewards: [], bindings: 0, encryption_ready: true })
    api.save.mockImplementation(async (c) => c)
  })
  afterEach(() => {
    try {
      // Development logs message errors instead of throwing as production does.
      expect(console.error).not.toHaveBeenCalled()
    } finally {
      vi.restoreAllMocks()
    }
  })
  it('saves health thresholds and recipients independently of price alerts', async () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })
    const wrapper = mount(QQBotView, { global: { plugins: [i18n] } })
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-notifications').trigger('click')
    expect((wrapper.get('[data-testid="health-enabled"]').element as HTMLInputElement).checked).toBe(false)
    await wrapper.get('[data-testid="health-enabled"]').setValue(true)
    await wrapper.get('[data-testid="health-groups"]').setValue('group\ngroup')
    await wrapper.get('[data-testid="health-minimum_samples"]').setValue(75)
    await wrapper.get('[data-testid="health-ttft_seconds"]').setValue(0)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({
      health_notifications: expect.objectContaining({ enabled: true, groups: ['group'], minimum_samples: 75, ttft_seconds: 0 }),
      price_notifications: expect.objectContaining({ enabled: false })
    }))
    wrapper.unmount()
  })
  it.each(['zh', 'en'] as const)('%s saves autonomous controls and shows decision reasons', async (locale) => {
    api.runtime.mockResolvedValue({ groups: [], jobs: [{ id: 'auto', kind: 'ambient_chat', group_id: 'group', trigger_reason: 'question', decision_reason: 'model_skip', status: 'skipped', input_tokens: 25, output_tokens: 5, created_at: '2026-10-20T01:00:00Z' }], autonomous_stats: [{ group_id: 'group', reason: 'model_calls', count: 1 }], rewards: [], bindings: 0, encryption_ready: true })
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const wrapper = mount(QQBotView, { global: { plugins: [i18n] } })
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-autonomous').trigger('click')
    expect((wrapper.get('[data-testid="autonomous-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('[data-testid="autonomous-sample_percent"]').element as HTMLInputElement).value).toBe('15')
    await wrapper.get('[data-testid="autonomous-enabled"]').setValue(true)
    await wrapper.get('[data-testid="autonomous-groups"]').setValue('group\ngroup')
    await wrapper.get('[data-testid="autonomous-names"]').setValue('CTAV\n小助手')
    await wrapper.get('[data-testid="autonomous-sample_percent"]').setValue(0)
    await wrapper.get('[data-testid="autonomous-daily_calls"]').setValue(30)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ autonomous: expect.objectContaining({ enabled: true, groups: ['group'], names: ['CTAV', '小助手'], sample_percent: 0, daily_calls: 30, mention_user: false }) }))
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.autonomous.reasons.model_skip'))
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.autonomous.reasons.model_calls'))
    expect((wrapper.get('[data-testid="autonomous-daily_calls"]').element as HTMLInputElement).value).toBe('30')
    wrapper.unmount()
  })
  it.each(['zh', 'en'] as const)('%s saves notification recipients and displays delivery failures', async (locale) => {
    api.runtime.mockResolvedValue({ groups: [], jobs: [{ id: 'notice', kind: 'price_notification', group_id: 'target', created_at: '2026-10-20T01:00:00Z', status: 'delivery_failed', error: 'QQ HTTP 403 / code 40034105' }], rewards: [], bindings: 0, encryption_ready: true })
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const wrapper = mount(QQBotView, { global: { plugins: [i18n] } })
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-notifications').trigger('click')
    expect((wrapper.get('[data-testid="price-notifications-enabled"]').element as HTMLInputElement).checked).toBe(false)
    expect((wrapper.get('[data-testid="price-notifications-scheduled"]').element as HTMLInputElement).checked).toBe(true)
    await wrapper.get('[data-testid="price-notifications-enabled"]').setValue(true)
    await wrapper.get('[data-testid="price-notification-groups"]').setValue('target\ntarget')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ price_notifications: { enabled: true, scheduled: true, groups: ['target'] } }))
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.priceNotifications.status.delivery_failed'))
    expect(wrapper.text()).toContain('40034105')
    wrapper.unmount()
  })
  it.each(['zh', 'en'] as const)('%s renders settings and disables probes until changes are saved', async (locale) => {
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const wrapper = mount(QQBotView, { global: { plugins: [i18n], stubs: { AppLayout: { template: '<main><slot /></main>' } } } })
    await flushPromises()
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.eventsHelp'))
    expect(wrapper.findAll('h2').map(heading => heading.text())).not.toContain(i18n.global.t('qqbot.inviteSettings'))
    expect(wrapper.text()).not.toContain('qqbot.eventsHelp')
    if (locale === 'zh') expect(wrapper.text()).toContain('活动指令和生图无需 @，普通聊天需 @ 机器人')
    expect((wrapper.find('input[type="password"]').element as HTMLInputElement).value).toBe('')
    const chatButton = () => wrapper.findAll('button').find(b => b.text() === i18n.global.t('qqbot.test_chat'))!
    expect(chatButton().attributes('disabled')).toBeUndefined()
    await wrapper.get('#qqbot-settings-tab-chat').trigger('click')
    await wrapper.find('input[placeholder="https://api.example.com/v1"]').setValue('https://other.invalid/v1')
    expect(chatButton().attributes('disabled')).toBeDefined()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ api_key: '', app_secret: '', base_url: 'https://other.invalid/v1' }))
  })

  it.each(['zh', 'en'] as const)('%s saves and reloads the shared history count, including zero', async (locale) => {
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const mountView = () => mount(QQBotView, { global: { plugins: [i18n] } })
    let wrapper = mountView()
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-chat').trigger('click')
    const input = () => wrapper.get('[data-testid="history-messages"]')
    expect((input().element as HTMLInputElement).value).toBe('12')
    expect(input().attributes('max')).toBe('100')
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.historyHelp'))
    for (const count of [25, 0]) {
      await input().setValue(count)
      await wrapper.find('form').trigger('submit')
      await flushPromises()
      expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ history_messages: count, system_prompt: 'Helpful' }))
      const saved = api.save.mock.calls.at(-1)![0]
      expect(saved).not.toHaveProperty('history_turns')
      api.config.mockResolvedValue(saved)
      wrapper.unmount()
      wrapper = mountView()
      await flushPromises()
      await wrapper.get('#qqbot-settings-tab-chat').trigger('click')
      expect((input().element as HTMLInputElement).value).toBe(String(count))
    }
    wrapper.unmount()
  })

  it('deletes an automatically discovered group without changing the allowed group input', async () => {
    api.config.mockResolvedValue({ enabled: false, app_id: 'app', app_secret: '', app_secret_set: true, allowed_groups: ['allowed-group'], base_url: 'https://example.invalid/v1', api_key: '', api_key_set: true, chat_model: 'model', chat_protocol: 'responses', image_enabled: false, image_size: '1024x1024', system_prompt: 'Helpful', max_output_tokens: 2048, history_messages: 12 })
    api.runtime.mockResolvedValue({ groups: [{ id: 'discovered-group', last_event: 'GROUP_AT_MESSAGE_CREATE', last_seen_at: '2026-10-15T00:00:00Z' }], jobs: [], rewards: [], bindings: 0, encryption_ready: true })
    api.deleteDiscoveredGroup.mockResolvedValue({ deleted: true })
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { zh, en } })
    const wrapper = mount(QQBotView, { global: { plugins: [i18n], stubs: { AppLayout: { template: '<main><slot /></main>' } } } })
    await flushPromises()

    const deleteButton = wrapper.findAll('button').find(button => button.text() === i18n.global.t('common.delete'))!
    await deleteButton.trigger('click')
    await flushPromises()

    expect(api.deleteDiscoveredGroup).toHaveBeenCalledWith('discovered-group')
    expect(wrapper.text()).not.toContain('discovered-group')
    expect((wrapper.find('textarea').element as HTMLTextAreaElement).value).toBe('allowed-group')
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.discoveredGroupDeleted'))
  })

  it.each(['zh', 'en'] as const)('%s adds, saves, reloads, disables and deletes custom commands', async (locale) => {
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const mountView = () => mount(QQBotView, { global: { plugins: [i18n] } })
    let wrapper = mountView()
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-commands').trigger('click')
    const commands = () => wrapper.find(`section[aria-label="${i18n.global.t('qqbot.customCommands')}"]`)
    expect(commands().text()).toContain(i18n.global.t('qqbot.noCustomCommands'))
    await commands().find('button').trigger('click')
    await commands().find('input[placeholder="/a"]').setValue('/a')
    await commands().find('textarea').setValue('第一行\n第二行')
    const probe = () => wrapper.findAll('button').find(button => button.text() === i18n.global.t('qqbot.test_chat'))!
    expect(probe().attributes('disabled')).toBeDefined()
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({
      custom_commands: [{ command: '/a', reply: '第一行\n第二行', enabled: true }]
    }))
    expect(probe().attributes('disabled')).toBeUndefined()

    api.config.mockResolvedValue(api.save.mock.calls[0][0])
    wrapper.unmount()
    wrapper = mountView()
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-commands').trigger('click')
    expect((commands().find('textarea').element as HTMLTextAreaElement).value).toBe('第一行\n第二行')
    await commands().find('input[type="checkbox"]').setValue(false)
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({
      custom_commands: [{ command: '/a', reply: '第一行\n第二行', enabled: false }]
    }))
    await commands().find('fieldset button').trigger('click')
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenLastCalledWith(expect.objectContaining({ custom_commands: [] }))
    expect(commands().findAll('fieldset')).toHaveLength(0)
    wrapper.unmount()
  })

  it('keeps the custom command draft when the server rejects a conflicting name', async () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })
    const wrapper = mount(QQBotView, { global: { plugins: [i18n] } })
    await flushPromises()
    await wrapper.get('#qqbot-settings-tab-commands').trigger('click')
    const commands = wrapper.find(`section[aria-label="${i18n.global.t('qqbot.customCommands')}"]`)
    await commands.find('button').trigger('click')
    await commands.find('input[placeholder="/a"]').setValue('/签到')
    await commands.find('textarea').setValue('固定回复')
    api.save.mockRejectedValueOnce({ message: '自定义指令 /签到 与内置指令重名' })
    await wrapper.find('form').trigger('submit')
    await flushPromises()
    expect(wrapper.find('[role="alert"]').text()).toContain('与内置指令重名')
    expect((commands.find('input[placeholder="/a"]').element as HTMLInputElement).value).toBe('/签到')
    expect((commands.find('textarea').element as HTMLTextAreaElement).value).toBe('固定回复')
    wrapper.unmount()
  })

  it('preserves edits across tabs and saves all categories together', async () => {
    const i18n = createI18n({ legacy: false, locale: 'zh', messages: { zh, en } })
    const wrapper = mount(QQBotView, { attachTo: document.body, global: { plugins: [i18n] } })
    await flushPromises()

    expect(wrapper.findAll('[role="tabpanel"]').filter(panel => panel.isVisible()).map(panel => panel.attributes('id'))).toEqual(['qqbot-settings-panel-connection'])
    await wrapper.get('#qqbot-settings-panel-connection input[autocomplete="off"]').setValue('updated-app')
    for (const tab of wrapper.findAll('[role="tab"]')) {
      await tab.trigger('click')
      expect(tab.attributes('aria-selected')).toBe('true')
      const visiblePanels = wrapper.findAll('[role="tabpanel"]').filter(panel => panel.isVisible())
      expect(visiblePanels).toHaveLength(1)
      expect(visiblePanels[0]!.attributes('id')).toBe(tab.attributes('aria-controls'))
    }
    await wrapper.get('#qqbot-settings-tab-autonomous').trigger('click')
    await wrapper.get('[data-testid="autonomous-names"]').setValue('New bot name')
    await wrapper.get('#qqbot-settings-tab-chat').trigger('click')
    await wrapper.get('[data-testid="history-messages"]').setValue(25)
    await wrapper.get('#qqbot-settings-tab-connection').trigger('click')
    expect((wrapper.get('#qqbot-settings-panel-connection input[autocomplete="off"]').element as HTMLInputElement).value).toBe('updated-app')
    expect(api.save).not.toHaveBeenCalled()
    expect(api.probe).not.toHaveBeenCalled()

    await wrapper.get('form').trigger('submit')
    await flushPromises()
    expect(api.save).toHaveBeenCalledWith(expect.objectContaining({ app_id: 'updated-app', history_messages: 25, autonomous: expect.objectContaining({ names: ['New bot name'] }) }))
    expect(wrapper.get('#qqbot-settings-tab-connection').attributes('aria-selected')).toBe('true')
    wrapper.unmount()
  })

  it.each(['chat', 'autonomous', 'commands'] as const)('opens the %s tab when a hidden field is invalid before saving', async (tab) => {
    const i18n = createI18n({ legacy: false, locale: 'en', messages: { zh, en } })
    const wrapper = mount(QQBotView, { attachTo: document.body, global: { plugins: [i18n] } })
    await flushPromises()
    await wrapper.get(`#qqbot-settings-tab-${tab}`).trigger('click')
    if (tab === 'chat') await wrapper.get('[data-testid="history-messages"]').setValue(101)
    if (tab === 'autonomous') await wrapper.get('[data-testid="autonomous-enabled"]').setValue(true)
    if (tab === 'commands') await wrapper.get('#qqbot-settings-panel-commands button').trigger('click')
    const panel = wrapper.get(`#qqbot-settings-panel-${tab}`)
    const input = panel.get('input:invalid, textarea:invalid').element as HTMLInputElement | HTMLTextAreaElement
    const reportValidity = vi.spyOn(input, 'reportValidity')
    await wrapper.get('#qqbot-settings-tab-connection').trigger('click')
    expect(panel.isVisible()).toBe(false)

    await wrapper.get('form').trigger('submit')
    await flushPromises()

    expect(api.save).not.toHaveBeenCalled()
    expect(panel.isVisible()).toBe(true)
    expect(wrapper.get(`#qqbot-settings-tab-${tab}`).attributes('aria-selected')).toBe('true')
    expect(reportValidity).toHaveBeenCalledOnce()
    wrapper.unmount()
  })
})
