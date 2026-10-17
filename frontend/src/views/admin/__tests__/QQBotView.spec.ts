import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import QQBotView from '../QQBotView.vue'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

const api = vi.hoisted(() => ({ config: vi.fn(), save: vi.fn(), runtime: vi.fn(), deleteDiscoveredGroup: vi.fn(), probe: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
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
})
