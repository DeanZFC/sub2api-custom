import { mount, flushPromises, type VueWrapper } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import QQCommandInventory from '../QQCommandInventory.vue'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'

const commands = vi.hoisted(() => vi.fn())
vi.mock('@/api/qqbot', () => ({ qqbotAPI: { commands } }))
const wrappers: VueWrapper[] = []

describe('QQ command inventory', () => {
  beforeEach(() => {
    commands.mockResolvedValue([
      { command: '/举报垃圾信息', usage: '/举报垃圾信息', description: '举报引用的垃圾消息', builtin: true },
      { command: '/faq', usage: '/faq', description: 'Custom reply', builtin: false, reply: '<img src="bad" />' }
    ])
  })
  afterEach(() => { wrappers.splice(0).forEach(wrapper => wrapper.unmount()); vi.clearAllMocks() })

  it.each(['zh', 'en'] as const)('%s explains quoted reports and conservative review without duplicating API commands', async locale => {
    const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
    const wrapper = mount(QQCommandInventory, { global: { plugins: [i18n] } })
    wrappers.push(wrapper)
    await flushPromises()
    expect(wrapper.findAll('[data-testid="qq-command-inventory-item"]')).toHaveLength(2)
    expect(wrapper.get('[data-testid="qq-report-command-help"]').text()).toBe(i18n.global.t('qqbot.moderation.reportCommandHelp'))
    expect(wrapper.get('[data-testid="qq-report-command-help"]').text()).toContain('已收到举报，正在评估。')
    expect(wrapper.text()).toContain(i18n.global.t('qqbot.moderation.reportSafetyHelp'))
    expect(wrapper.text()).toContain('<img src="bad" />')
    expect(wrapper.find('img').exists()).toBe(false)
    expect(commands).toHaveBeenCalledOnce()
  })
})
