import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import { createMemoryHistory, createRouter } from 'vue-router'
import QQBotView from '../QQBotView.vue'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import { configSections, pickSection, cloneConfig } from '@/components/admin/qqbot/configSections'
import { defaultModerationConfig, type QQBotConfig, type QQMemberOption } from '@/api/qqbot'
const api = vi.hoisted(() => ({ config: vi.fn(), saveSection: vi.fn(), runtime: vi.fn(), bindings: vi.fn(), members: vi.fn(), deleteDiscoveredGroup: vi.fn(), probe: vi.fn(), configHistory: vi.fn(), commands: vi.fn() }))
vi.mock('@/api/qqbot', async original => ({ ...await original<typeof import('@/api/qqbot')>(), qqbotAPI: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
const wrappers: VueWrapper[] = []
let server: QQBotConfig
const memberOptions: QQMemberOption[] = [
 { group_id: 'group', member_id: 'trusted-member', nickname: 'Trusted member', email: '', message_preview: '', last_seen_at: '', group_label: 'Primary group' },
 { group_id: 'group', member_id: 'existing-member', nickname: 'Existing member', email: '', message_preview: '', last_seen_at: '', group_label: 'Primary group' },
 { group_id: 'group', member_id: 'recent-member', nickname: '', email: '', message_preview: '我是小王，确认身份 4826', last_seen_at: '2026-10-23T10:30:00+08:00', group_label: 'Primary group' },
 { group_id: 'second-group', member_id: 'bound-member', nickname: 'Alice', email: 'alice@example.com', message_preview: '', last_seen_at: '', group_label: 'Second group' }
]
function memberKey(group: string, member: string) { return JSON.stringify([group, member]) }
async function mountView(locale: 'zh' | 'en' = 'zh', path = '/admin/qqbot/config') {
 const router = createRouter({ history: createMemoryHistory(), routes: [
  { path: '/admin/qqbot/config', component: QQBotView }, { path: '/admin/qqbot/groups', component: QQBotView }, { path: '/other', component: { template: '<p>Other</p>' } }
 ] })
 await router.push(path)
 const wrapper = mount({ template: '<RouterView />' }, { attachTo: document.body, global: { plugins: [createI18n({ legacy: false, locale, messages: { zh, en } }), router] } })
 wrappers.push(wrapper); await flushPromises(); return wrapper
}
async function category(w: VueWrapper, text: string) { await w.findAll('button').find(button => button.text() === text)!.trigger('click') }

describe('QQ module settings', () => {
 beforeEach(() => {
  vi.resetAllMocks()
  vi.spyOn(console, 'error').mockImplementation(() => {})
  server = { enabled: false, app_id: 'app', app_secret: '', app_secret_set: true, sandbox: false, allowed_groups: ['group', 'other-group'], group_policies: [], base_url: 'https://example.invalid/v1', api_key: '', api_key_set: true, chat_model: 'model', chat_protocol: 'responses', image_enabled: false, image_base_url: '', image_api_key: '', image_api_key_set: false, image_model: '', image_size: '1024x1024', system_prompt: 'Helpful', max_output_tokens: 2048, history_messages: 12, daily_chat_limit: 30, daily_image_limit: 2, total_daily_chat_limit: 500, total_daily_image_limit: 20, cooldown_seconds: 5, checkin_reward: 0, invite_reward: 0, invite_daily_limit: 10, custom_commands: [], section_versions: Object.fromEntries(Object.keys(configSections).map(key => [key, `${key}-1`])) }
  api.config.mockImplementation(async () => cloneConfig(server))
  api.runtime.mockResolvedValue({ groups: [], jobs: [], rewards: [], bindings: 0, encryption_ready: true })
  api.members.mockImplementation(async (params: { group_id?: string; member_id?: string; search?: string }) => {
   const items = memberOptions.filter(member => (!params.group_id || member.group_id === params.group_id) && (!params.member_id || member.member_id === params.member_id) && (!params.search || [member.nickname, member.email, member.message_preview].join(' ').toLowerCase().includes(params.search.toLowerCase())))
   return { app_id: 'app', items, total: items.length, page: 1, page_size: 20 }
  })
  api.commands.mockResolvedValue([]); api.configHistory.mockResolvedValue([])
  api.saveSection.mockImplementation(async (section: keyof typeof configSections, _version: string, draft: QQBotConfig) => { Object.assign(server, pickSection(draft, section)); server.section_versions![section] += '-next'; return cloneConfig(server) })
 })
 afterEach(() => { wrappers.splice(0).forEach(w => w.unmount()); try { expect(console.error).not.toHaveBeenCalled() } finally { vi.restoreAllMocks() } })
 it.each(['zh', 'en'] as const)('%s groups navigation and saves only the selected module', async locale => {
  const w = await mountView(locale)
  expect(w.findAll('[role="tab"]')).toHaveLength(3)
  expect(w.get('#qqbot-settings-panel-connection').isVisible()).toBe(true)
  await w.get('#qqbot-settings-panel-connection input[autocomplete="off"]').setValue('unsaved-app')
  await w.get('#qqbot-settings-tab-chat').trigger('click')
  await w.get('[data-testid="history-messages"]').setValue(25)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(api.saveSection).toHaveBeenCalledWith('chat', 'chat-1', expect.objectContaining({ history_messages: 25 }))
  expect(server.app_id).toBe('app')
  await w.get('#qqbot-settings-tab-connection').trigger('click')
  expect((w.get('#qqbot-settings-panel-connection input[autocomplete="off"]').element as HTMLInputElement).value).toBe('unsaved-app')
  expect(w.get('#qqbot-settings-tab-connection').text()).toContain('•')
 })
 it('does not validate unrelated drafts and keeps original revisions for unsaved modules', async () => {
  const w = await mountView()
  await w.get('#qqbot-settings-tab-chat').trigger('click')
  await w.get('[data-testid="history-messages"]').setValue(101)
  server.chat_model = 'changed-elsewhere'; server.section_versions!.chat = 'chat-2'
  await w.get('#qqbot-settings-tab-connection').trigger('click')
  await w.get('#qqbot-settings-panel-connection input[type="checkbox"]').setValue(true)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(api.saveSection).toHaveBeenCalledWith('connection', 'connection-1', expect.anything())
  await w.get('#qqbot-settings-tab-chat').trigger('click')
  await w.get('[data-testid="history-messages"]').setValue(30)
  api.saveSection.mockRejectedValueOnce(new Error('此模块已被其他页面更新'))
  await w.get('form').trigger('submit'); await flushPromises()
  expect(api.saveSection).toHaveBeenLastCalledWith('chat', 'chat-1', expect.objectContaining({ chat_model: 'model' }))
  expect(w.get('[role="alert"]').text()).toContain('其他页面')
  expect((w.get('[data-testid="history-messages"]').element as HTMLInputElement).value).toBe('30')
 })
 it('validates the active module and protects drafts on refresh and navigation', async () => {
  const w = await mountView()
  await w.get('#qqbot-settings-tab-chat').trigger('click')
  await w.get('[data-testid="history-messages"]').setValue(101)
  const input = w.get('[data-testid="history-messages"]').element as HTMLInputElement
  const validity = vi.spyOn(input, 'reportValidity')
  await w.get('form').trigger('submit'); expect(validity).toHaveBeenCalledOnce(); expect(api.saveSection).not.toHaveBeenCalled()
  const event = new Event('beforeunload', { cancelable: true }); window.dispatchEvent(event); expect(event.defaultPrevented).toBe(true)
  vi.spyOn(window, 'confirm').mockReturnValue(false)
  await w.findAll('button').find(b => b.text() === '重新加载本模块')!.trigger('click'); await flushPromises()
  expect((input).value).toBe('101')
  await w.vm.$router.push('/other'); expect(w.vm.$route.path).toBe('/admin/qqbot/config')
 })
 it('saves an independent chat switch and retains zero shared history', async () => {
  const w = await mountView()
  await w.get('#qqbot-settings-tab-chat').trigger('click')
  await w.get('#qqbot-settings-panel-chat input[type="checkbox"]').setValue(true)
  await w.get('[data-testid="history-messages"]').setValue(0)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.chat_disabled).toBe(true); expect(server.history_messages).toBe(0)
 })
 it('requires explicit all-groups selection and saves notification scope', async () => {
  const w = await mountView(); await category(w, '通知与额度'); await w.get('#qqbot-settings-tab-notifications').trigger('click')
  const pickers = w.get('#qqbot-settings-panel-notifications').findAllComponents({ name: 'QQGroupPicker' })
  await pickers[0]!.findAll('input[type="checkbox"]')[1]!.setValue(true)
  await w.get('[data-testid="price-notifications-enabled"]').setValue(true)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.price_notifications?.groups).toEqual(['group'])
  await pickers[0]!.findAll('input[type="checkbox"]')[1]!.setValue(false)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.price_notifications?.groups).toEqual(['group'])
 })
 it('presets keep autonomous participation opt-in and bounded', async () => {
  const w = await mountView(); await category(w, '对话与指令')
  await w.findAll('button').find(b => b.text() === '安静')!.trigger('click')
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.autonomous).toMatchObject({ enabled: false, groups: [], sample_percent: 5, daily_calls: 15, daily_replies: 8 })
 })
 it('saves custom replies and preserves drafts on validation failure', async () => {
  const w = await mountView(); await category(w, '对话与指令'); await w.get('#qqbot-settings-tab-commands').trigger('click')
  const panel = w.get('#qqbot-settings-panel-commands'); await panel.findAll('button')[0]!.trigger('click')
  await panel.get('input[placeholder="/a"]').setValue('/签到'); await panel.get('textarea').setValue('固定回复')
  api.saveSection.mockRejectedValueOnce(new Error('与内置指令重名'))
  await w.get('form').trigger('submit'); await flushPromises()
  expect(w.get('[role="alert"]').text()).toContain('重名')
  await panel.get('input[placeholder="/a"]').setValue('/faq'); await w.get('form').trigger('submit'); await flushPromises()
  expect(server.custom_commands).toEqual([{ command: '/faq', reply: '固定回复', enabled: true }])
 })
 it('shows and saves moderation only on the group page', async () => {
  const w = await mountView('zh', '/admin/qqbot/groups')
  expect(w.findAll('[role="tab"]')).toHaveLength(3)
  await w.get('#qqbot-settings-tab-moderation').trigger('click')
  await w.get('[data-testid="moderation-enabled"]').setValue(true)
  await w.findAll('[data-testid="moderation-group"]')[0]!.setValue(true)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.moderation).toMatchObject({ enabled: true, groups: ['group'] })
 })
 it('merges report defaults into a legacy moderation config without changing rules or exemptions', async () => {
  api.config.mockResolvedValue({ ...cloneConfig(server), moderation: {
   enabled: true, groups: ['group'], repeat_window_seconds: 12, repeat_count: 6,
   burst_window_seconds: 8, burst_count: 9, mute_minutes: 7,
   repeat_offense_minutes: 45, escalated_mute_minutes: 60,
   exemptions: [{ group_id: 'group', member_id: 'existing-member' }]
  } })
  const w = await mountView('zh', '/admin/qqbot/groups')
  await w.get('#qqbot-settings-tab-moderation').trigger('click')
  expect((w.get('[data-testid="moderation-report-enabled"]').element as HTMLInputElement).checked).toBe(false)
  expect((w.get('[data-testid="moderation-report-mute-minutes"]').element as HTMLInputElement).value).toBe('5')
  expect((w.get('[data-testid="moderation-repeat_count"]').element as HTMLInputElement).value).toBe('6')
  expect(w.get('[data-testid="moderation-exempt-member"]').text()).toContain('existing-member')
  await w.get('[data-testid="moderation-report-enabled"]').setValue(true)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.moderation).toMatchObject({ enabled: true, report_enabled: true, report_mute_minutes: 5,
   repeat_window_seconds: 12, repeat_count: 6, burst_window_seconds: 8, burst_count: 9,
   mute_minutes: 7, repeat_offense_minutes: 45, escalated_mute_minutes: 60,
   groups: ['group'], exemptions: [{ group_id: 'group', member_id: 'existing-member' }]
  })
 })
 it.each(['zh', 'en'] as const)('%s saves reports independently of chat and flooding while preserving shared exemptions', async locale => {
  server.chat_disabled = true
  const w = await mountView(locale, '/admin/qqbot/groups')
  await w.get('#qqbot-settings-tab-moderation').trigger('click')
  expect(w.get('[data-testid="moderation-shared-groups"]').attributes('disabled')).toBeDefined()
  await w.get('[data-testid="moderation-report-enabled"]').setValue(true)
  expect(w.get('[data-testid="moderation-shared-groups"]').attributes('disabled')).toBeUndefined()
  expect(w.get('[data-testid="moderation-flood-fields"]').attributes('disabled')).toBeDefined()
  await w.findAll('[data-testid="moderation-group"]')[0]!.setValue(true)
  await w.get('[data-testid="moderation-add-exemption"]').trigger('click')
  await flushPromises()
  await w.get('[data-testid="moderation-exempt-member"] [data-testid="qq-member-select"]').setValue(memberKey('group', 'trusted-member'))
  await w.get('[data-testid="moderation-report-mute-minutes"]').setValue(8)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.moderation).toMatchObject({ enabled: false, report_enabled: true, report_mute_minutes: 8,
   groups: ['group'], exemptions: [{ group_id: 'group', member_id: 'trusted-member', nickname: 'Trusted member' }]
  })
  expect(server.chat_disabled).toBe(true)
  await w.get('[data-testid="moderation-enabled"]').setValue(true)
  await w.get('[data-testid="moderation-report-enabled"]').setValue(false)
  expect(w.get('[data-testid="moderation-shared-groups"]').attributes('disabled')).toBeUndefined()
  expect(w.get('[data-testid="moderation-report-mute-minutes"]').attributes('disabled')).toBeDefined()
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.moderation).toMatchObject({ enabled: true, report_enabled: false, report_mute_minutes: 8,
   groups: ['group'], exemptions: [{ group_id: 'group', member_id: 'trusted-member', nickname: 'Trusted member' }]
  })
  await w.get('[data-testid="moderation-enabled"]').setValue(false)
  expect(w.get('[data-testid="moderation-shared-exemptions"]').attributes('disabled')).toBeDefined()
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.moderation?.exemptions).toEqual([{ group_id: 'group', member_id: 'trusted-member', nickname: 'Trusted member' }])
  expect(server.moderation?.groups).toEqual(['group'])
 })
 it('validates the report mute duration as an integer from one to sixty minutes', async () => {
  server.moderation = { ...defaultModerationConfig(), report_enabled: true, groups: ['group'] }
  const w = await mountView('zh', '/admin/qqbot/groups')
  await w.get('#qqbot-settings-tab-moderation').trigger('click')
  const input = w.get('[data-testid="moderation-report-mute-minutes"]')
  const reportValidity = vi.spyOn(input.element as HTMLInputElement, 'reportValidity')
  for (const value of [0, 61, 1.5]) {
   await input.setValue(value)
   await w.get('form').trigger('submit'); await flushPromises()
   expect(api.saveSection).not.toHaveBeenCalled()
  }
  expect(reportValidity).toHaveBeenCalledTimes(3)
  await input.setValue(60)
  await w.get('form').trigger('submit'); await flushPromises()
  expect(server.moderation?.report_mute_minutes).toBe(60)
 })
  it.each(['zh', 'en'] as const)('%s selects group admins by recent messages and bound identity and supports removal', async locale => {
    server.allowed_groups = ['group', 'second-group']
    server.group_policies = [{ group_id: 'group', label: 'Primary group', disable_chat: false, disable_images: false, disable_activities: false, disable_notifications: false, chat_daily_limit: null, image_daily_limit: null }]
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    let wrapper = await mountView(locale, '/admin/qqbot/groups')
    await wrapper.get('#qqbot-settings-tab-group-admins').trigger('click')
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.groupAdmins.help'))
    await wrapper.get('[data-testid="add-group-admin"]').trigger('click'); await flushPromises()
    let first = wrapper.findAll('[data-testid="group-admin-entry"]')[0]!
    expect(first.get('[data-testid="group-admin-group"]').text()).toContain('Primary group')
    expect(first.text()).toContain('我是小王，确认身份 4826')
    await first.get('[data-testid="qq-member-search"]').setValue('4826')
    await first.get('[data-testid="qq-member-search"]').trigger('keydown.enter'); await flushPromises()
    expect(api.members).toHaveBeenCalledWith(expect.objectContaining({ app_id: 'app', group_id: 'group', search: '4826' }))
    await first.get('[data-testid="qq-member-select"]').setValue(memberKey('group', 'recent-member'))
    await wrapper.get('[data-testid="add-group-admin"]').trigger('click'); await flushPromises()
    const second = wrapper.findAll('[data-testid="group-admin-entry"]')[1]!
    await second.get('[data-testid="group-admin-group"]').setValue('second-group'); await flushPromises()
    await second.get('[data-testid="qq-member-select"]').setValue(memberKey('second-group', 'bound-member'))
    expect(second.get('[data-testid="qq-member-selected"]').text()).toContain('alice@example.com')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    const saved = api.saveSection.mock.calls.at(-1)![2]
    expect(saved.group_admins).toEqual([
      { app_id: 'app', group_id: 'group', member_id: 'recent-member', nickname: '' },
      { app_id: 'app', group_id: 'second-group', member_id: 'bound-member', nickname: 'Alice' }
    ])
    expect(api.bindings).not.toHaveBeenCalled()
    wrapper.unmount()
    wrapper = await mountView(locale, '/admin/qqbot/groups')
    await wrapper.get('#qqbot-settings-tab-group-admins').trigger('click'); await flushPromises()
    expect(wrapper.findAll('[data-testid="group-admin-entry"]')).toHaveLength(2)
    first = wrapper.findAll('[data-testid="group-admin-entry"]')[0]!
    expect(first.get('[data-testid="qq-member-selected"]').text()).toContain('我是小王，确认身份 4826')
    await first.get('button').trigger('click')
    await wrapper.get('form').trigger('submit'); await flushPromises()
    expect(api.saveSection.mock.calls.at(-1)![2].group_admins).toEqual([saved.group_admins[1]])
  })


})
