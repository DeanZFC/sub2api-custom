import { mount, flushPromises } from '@vue/test-utils'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import QQBotRewardCodes from '../QQBotRewardCodes.vue'

const api = vi.hoisted(() => ({ rewardCodes: vi.fn(), createRewardCodes: vi.fn(), updateRewardCode: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
const code = 'ABCD-EFGH-IJKL-MNOP-QRST-UVWX-YZ23-4567'
function setup(locale = 'zh') {
  const i18n = createI18n({ legacy: false, locale, messages: { zh, en } })
  return { wrapper: mount(QQBotRewardCodes, { global: { plugins: [i18n] } }), t: i18n.global.t }
}

describe('QQ reward code management', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    api.rewardCodes.mockResolvedValue({ items: [], total: 0, page: 1, page_size: 20 })
    api.createRewardCodes.mockResolvedValue([{ id: 1, code }])
    api.updateRewardCode.mockResolvedValue({ success: true })
  })
  it.each(['zh', 'en'])('%s generates chances and keeps full codes if refreshing records fails', async locale => {
    const { wrapper: w, t } = setup(locale)
    await flushPromises()
    await w.get('[data-testid="reward-batch"]').setValue('Launch')
    await w.get('[data-testid="reward-type"]').setValue('chances')
    await w.get('[data-testid="reward-quantity"]').setValue('3')
    await w.get('[data-testid="reward-target"]').setValue('42')
    api.rewardCodes.mockRejectedValueOnce(new Error('List failed'))
    await w.findAll('form')[0]!.trigger('submit'); await flushPromises()
    expect(api.createRewardCodes).toHaveBeenCalledWith(expect.objectContaining({ reward_type: 'chances', amount: 0, quantity: 3, target_user_id: 42, expires_at: null }))
    expect((w.get('[data-testid="generated-codes"] textarea').element as HTMLTextAreaElement).value).toBe(code)
    expect(w.text()).toContain('List failed')
    expect(w.text()).toContain(t('qqbot.redemption.once'))
    expect(w.get('[data-testid="reward-generate"]').attributes('disabled')).toBeDefined()
    await w.findAll('form')[0]!.trigger('submit')
    expect(api.createRewardCodes).toHaveBeenCalledTimes(1)
    const clear = w.get('[data-testid="generated-codes"]').findAll('button').find(b => b.text() === t('qqbot.redemption.clear'))!
    await clear.trigger('click')
    expect(w.find('[data-testid="generated-codes"]').exists()).toBe(false)
    w.unmount()
  })
  it('rejects invalid amounts, counts and expiry before generation', async () => {
    const { wrapper: w } = setup(); await flushPromises()
    await w.get('[data-testid="reward-batch"]').setValue('test')
    for (const amount of ['0', '-1', '1001', '0.001']) {
      await w.get('[data-testid="reward-amount"]').setValue(amount)
      await w.findAll('form')[0]!.trigger('submit')
      expect(api.createRewardCodes).not.toHaveBeenCalled()
    }
    await w.get('[data-testid="reward-amount"]').setValue('10')
    await w.get('[data-testid="reward-count"]').setValue('201')
    await w.findAll('form')[0]!.trigger('submit'); expect(api.createRewardCodes).not.toHaveBeenCalled()
    await w.get('[data-testid="reward-count"]').setValue('1')
    await w.get('[data-testid="reward-expiry"]').setValue('2020-01-01T00:00')
    await w.findAll('form')[0]!.trigger('submit'); expect(api.createRewardCodes).not.toHaveBeenCalled()
    w.unmount()
  })
  it('requires explicit fulfillment confirmation and preserves the note after failure', async () => {
    api.rewardCodes.mockResolvedValue({ items: [{ id: 7, app_id: 'app', code_hint: 'ABCD23', batch_name: 'launch', reward_type: 'physical', amount: 0, quantity: 1, prize_name: 'Cup', status: 'pending', email: 'test@example.com', user_id: 8, member_id: 'member', expires_at: null, target_user_id: null, redeemed_at: '2026-10-17T00:00:00Z', fulfilled_at: null, fulfillment_note: '' }], total: 1, page: 1, page_size: 20 })
    const { wrapper: w, t } = setup(); await flushPromises()
    await w.get('tbody button').trigger('click')
    expect(api.updateRewardCode).not.toHaveBeenCalled()
    const confirmation = w.findAll('form')[2]!
    await confirmation.get('textarea').setValue('Delivered in person')
    api.updateRewardCode.mockRejectedValueOnce(new Error('Try again'))
    await confirmation.trigger('submit'); await flushPromises()
    expect(api.updateRewardCode).toHaveBeenCalledWith(7, 'fulfill', 'Delivered in person')
    expect(confirmation.get('textarea').element.value).toBe('Delivered in person')
    await confirmation.trigger('submit'); await flushPromises()
    expect(w.text()).toContain(t('common.saved'))
    expect(w.findAll('form')).toHaveLength(2)
    w.unmount()
  })
})
