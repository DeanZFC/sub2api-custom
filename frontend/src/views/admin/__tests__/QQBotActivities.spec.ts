import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import QQBotCheckinView from '../QQBotCheckinView.vue'
import QQBotLotteryView from '../QQBotLotteryView.vue'
import QQBotRuntimeView from '../QQBotRuntimeView.vue'

const api = vi.hoisted(() => ({ checkinConfig: vi.fn(), saveCheckin: vi.fn(), lotteryConfig: vi.fn(), saveLottery: vi.fn(), lotteryDraws: vi.fn(), lotteryState: vi.fn(), runtime: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
function i18n(locale: 'zh' | 'en') { return createI18n({ legacy: false, locale, messages: { zh, en } }) }

describe('QQ activity pages with real translations', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.spyOn(console, 'error').mockImplementation(() => {})
    api.checkinConfig.mockResolvedValue({ enabled: true, min_amount: 0.1, max_amount: 0.5 })
    api.saveCheckin.mockImplementation(async c => c)
    api.lotteryConfig.mockResolvedValue({ enabled: false, title: 'Lottery', recharge_step: 10, chances_per_step: 1, prizes: [{ name: 'Win', amount: 1, probability_bps: 1000 }, { name: 'Thanks', amount: 0, probability_bps: 9000 }] })
    api.saveLottery.mockImplementation(async c => c)
    api.lotteryDraws.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    api.lotteryState.mockResolvedValue({ user_id: 7, recharge_total: 35, earned: 3, used: 1, remaining: 2, next_recharge: 5 })
    api.runtime.mockResolvedValue({ groups: [], jobs: [], rewards: [{ id: 1, user_id: 7, kind: 'lottery', amount: 0, created_at: '2026-10-15' }], bindings: 2, encryption_ready: true })
  })
  afterEach(() => { try { expect(console.error).not.toHaveBeenCalled() } finally { vi.restoreAllMocks() } })
  it.each(['zh', 'en'] as const)('%s saves check-in range independently and rejects reversed endpoints', async locale => {
    const translation = i18n(locale)
    const w = mount(QQBotCheckinView, { global: { plugins: [translation] } })
    await flushPromises()
    expect(w.text()).toContain('@CTAV 签到')
    await w.get('[data-testid="min-amount"]').setValue('0.7')
    await w.get('form').trigger('submit')
    expect(api.saveCheckin).not.toHaveBeenCalled()
    expect(w.get('[role="alert"]').text()).toBe(translation.global.t('qqbot.invalidRange'))
    await w.get('[data-testid="max-amount"]').setValue('0.9')
    await w.get('form').trigger('submit')
    await flushPromises()
    expect(api.saveCheckin).toHaveBeenCalledWith({ enabled: true, min_amount: 0.7, max_amount: 0.9 })
    expect(w.get('[role="status"]').text()).toBe(translation.global.t('common.saved'))
  })
  it.each(['zh', 'en'] as const)('%s validates prize probability totals and saves the cumulative rule', async locale => {
    const translation = i18n(locale)
    const w = mount(QQBotLotteryView, { global: { plugins: [translation] } })
    await flushPromises()
    expect(w.text()).toContain('@CTAV 抽奖')
    expect(w.text()).toContain(translation.global.t('qqbot.historicalRechargeHelp'))
    const form = w.findAll('form')[0]!
    const save = () => form.get('button[type="submit"]')
    await w.get('[data-testid="probability-0"]').setValue('25.25')
    expect(save().attributes('disabled')).toBeDefined()
    await form.trigger('submit'); expect(api.saveLottery).not.toHaveBeenCalled()
    await w.get('[data-testid="probability-1"]').setValue('74.75')
    await w.get('[data-testid="recharge-step"]').setValue('20')
    await w.get('[data-testid="chances-per-step"]').setValue('2')
    expect(save().attributes('disabled')).toBeUndefined()
    await form.trigger('submit'); await flushPromises()
    expect(api.saveLottery).toHaveBeenCalledWith(expect.objectContaining({ recharge_step: 20, chances_per_step: 2, prizes: [{ name: 'Win', amount: 1, probability_bps: 2525 }, { name: 'Thanks', amount: 0, probability_bps: 7475 }] }))
    const lookup = w.findAll('form')[1]!
    await lookup.get('input').setValue('7'); await lookup.trigger('submit'); await flushPromises()
    expect(api.lotteryState).toHaveBeenCalledWith(7)
    expect(w.text()).toContain('$35.00000000')
    api.lotteryState.mockRejectedValueOnce(new Error('User not found'))
    await lookup.trigger('submit'); await flushPromises()
    expect(w.text()).not.toContain('$35.00000000')
    expect(w.text()).toContain('User not found')
  })
  it('searches draw history and preserves the applied search while paging', async () => {
    const translation = i18n('zh')
    const w = mount(QQBotLotteryView, { global: { plugins: [translation] } }); await flushPromises()
    api.lotteryDraws.mockResolvedValueOnce({ items: [{ id: 1, user_id: 7, email: 'test@example.com', member_id: 'member', prize_name: 'Thanks', amount: 0, remaining: 2, created_at: '2026-10-15' }], total: 21, page: 1, page_size: 20 })
    const search = w.findAll('form')[2]!
    await search.get('input').setValue('test@example.com'); await search.trigger('submit'); await flushPromises()
    expect(api.lotteryDraws).toHaveBeenLastCalledWith('test@example.com', 1)
    expect(w.text()).toContain('Thanks')
    await search.get('input').setValue('unsubmitted')
    await w.findAll('button').find(b => b.text() === translation.global.t('qqbot.nextPage'))!.trigger('click'); await flushPromises()
    expect(api.lotteryDraws).toHaveBeenLastCalledWith('test@example.com', 2)
  })
  it.each(['zh', 'en'] as const)('%s renders the standalone runtime page', async locale => {
    const translation = i18n(locale)
    const w = mount(QQBotRuntimeView, { global: { plugins: [translation] } }); await flushPromises()
    expect(w.text()).toContain(translation.global.t('qqbot.runtime'))
    expect(w.text()).toContain('lottery')
    expect(w.find('form').exists()).toBe(false)
  })
})
