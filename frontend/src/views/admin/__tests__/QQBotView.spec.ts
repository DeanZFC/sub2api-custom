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
    api.config.mockResolvedValue({ enabled: false, app_id: 'app', app_secret: '', app_secret_set: true, allowed_groups: [], base_url: 'https://example.invalid/v1', api_key: '', api_key_set: true, chat_model: 'model', chat_protocol: 'responses', image_enabled: false, image_size: '1024x1024', system_prompt: 'Helpful', max_output_tokens: 2048, history_turns: 6 })
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

  it('deletes an automatically discovered group without changing the allowed group input', async () => {
    api.config.mockResolvedValue({ enabled: false, app_id: 'app', app_secret: '', app_secret_set: true, allowed_groups: ['allowed-group'], base_url: 'https://example.invalid/v1', api_key: '', api_key_set: true, chat_model: 'model', chat_protocol: 'responses', image_enabled: false, image_size: '1024x1024', system_prompt: 'Helpful', max_output_tokens: 2048, history_turns: 6 })
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
})
