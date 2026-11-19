import { mount, flushPromises } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { createI18n } from 'vue-i18n'
import zh from '@/i18n/locales/zh'
import en from '@/i18n/locales/en'
import QQBotReportsView from '../QQBotReportsView.vue'
import type { QQRewardReport } from '@/api/qqbot'

const api = vi.hoisted(() => ({ reports: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))
vi.mock('@/components/layout/AppLayout.vue', () => ({ default: { template: '<main><slot /></main>' } }))
vi.mock('vue-chartjs', () => ({ Line: { template: '<div data-testid="report-chart" />' } }))
const metrics = { reward_count: 21, user_count: 2, balance_amount: 0.00000001, chances_quantity: 3, coupon_quantity: 2, pending_quantity: 1, fulfilled_quantity: 2 }
function result(page = 1): QQRewardReport {
  return { start_date: '2026-11-12', end_date: '2026-11-18', timezone: 'Asia/Shanghai', summary: { ...metrics },
    daily: [{ date: '2026-11-18', ...metrics }], by_kind: [{ kind: 'checkin', ...metrics }], top_users: [{ user_id: 7, nickname: '', email: 'recipient@example.com', ...metrics }],
    items: [{ id: 'r:1', kind: 'checkin', reward_type: 'balance', amount: 0.00000001, quantity: 0, prize_name: '', status: 'fulfilled', user_id: 7, email: 'recipient@example.com', nickname: '', member_id: 'member', group_id: 'group', created_at: '2026-11-17T16:00:00Z' }], total: 21, page, page_size: 20 }
}
async function setup(locale: 'zh' | 'en' = 'zh') {
  const translation = createI18n({ legacy: false, locale, messages: { zh, en } })
  const wrapper = mount(QQBotReportsView, { global: { plugins: [translation] } })
  await flushPromises()
  return { wrapper, t: translation.global.t }
}

describe('QQ reward reports', () => {
  beforeEach(() => {
    vi.resetAllMocks()
    vi.useFakeTimers({ toFake: ['Date'] })
    vi.setSystemTime(new Date('2026-11-17T16:30:00Z'))
    api.reports.mockResolvedValue(result())
  })
  afterEach(() => { vi.useRealTimers(); vi.restoreAllMocks() })

  it.each(['zh', 'en'] as const)('%s shows separate totals and preserves eight-decimal balances', async locale => {
    const { wrapper: w, t } = await setup(locale)
    expect(api.reports).toHaveBeenCalledWith({ start_date: '2026-11-12', end_date: '2026-11-18', group_id: '', kind: '', reward_type: '', search: '', page: 1, page_size: 20 })
    expect(w.get('[data-testid="report-summary"]').text()).toContain('$0.00000001')
    expect(w.text()).toContain(t('qqbot.reports.metrics.pending_quantity'))
    expect(w.text()).toContain('recipient@example.com')
    expect(w.text()).toContain(t('qqbot.reports.beijing'))
    w.unmount()
  })

  it('applies filters consistently to summaries and subsequent pages, ignoring unsubmitted changes', async () => {
    const { wrapper: w } = await setup()
    await w.get('[data-testid="report-kind"]').setValue('growth')
    await w.get('[data-testid="report-type"]').setValue('coupon')
    await w.get('[data-testid="report-group"]').setValue('group-filter')
    await w.get('[data-testid="report-search"]').setValue('  applied@example.com  ')
    await w.get('[data-testid="report-form"]').trigger('submit'); await flushPromises()
    expect(api.reports).toHaveBeenLastCalledWith(expect.objectContaining({ kind: 'growth', reward_type: 'coupon', group_id: 'group-filter', search: 'applied@example.com', page: 1 }))
    await w.get('[data-testid="report-search"]').setValue('unsaved')
    api.reports.mockResolvedValueOnce(result(2))
    await w.get('[data-testid="report-next"]').trigger('click'); await flushPromises()
    expect(api.reports).toHaveBeenLastCalledWith(expect.objectContaining({ search: 'applied@example.com', page: 2 }))
    w.unmount()
  })

  it('queries Beijing presets and rejects inverted or excessive date ranges', async () => {
    const { wrapper: w, t } = await setup()
    await w.get('[data-testid="report-preset-yesterday"]').trigger('click'); await flushPromises()
    expect(api.reports).toHaveBeenLastCalledWith(expect.objectContaining({ start_date: '2026-11-17', end_date: '2026-11-17', page: 1 }))
    await w.get('[data-testid="report-start"]').setValue('2026-11-19')
    await w.get('[data-testid="report-end"]').setValue('2026-11-18')
    await w.get('[data-testid="report-form"]').trigger('submit'); await flushPromises()
    expect(api.reports).toHaveBeenCalledTimes(2)
    expect(w.get('[role="alert"]').text()).toBe(t('qqbot.reports.invalidDates'))
    w.unmount()
  })

  it('ignores late responses and hides old totals on failures', async () => {
    const { wrapper: w } = await setup()
    let finish: (value: QQRewardReport) => void = () => {}
    api.reports.mockReturnValueOnce(new Promise(resolve => { finish = resolve }))
    await w.get('[data-testid="report-preset-today"]').trigger('click')
    api.reports.mockResolvedValueOnce({ ...result(), start_date: '2026-11-17', end_date: '2026-11-17' })
    await w.get('[data-testid="report-preset-yesterday"]').trigger('click'); await flushPromises()
    finish({ ...result(), start_date: '2026-11-18', end_date: '2026-11-18' }); await flushPromises()
    expect(w.get('[data-testid="report-period"]').text()).toContain('2026-11-17 — 2026-11-17')
    api.reports.mockRejectedValueOnce(new Error('Report failed'))
    await w.get('[data-testid="report-refresh"]').trigger('click'); await flushPromises()
    expect(w.get('[role="alert"]').text()).toBe('Report failed')
    expect(w.find('[data-testid="report-summary"]').exists()).toBe(false)
    expect(w.find('[data-testid="report-export"]').exists()).toBe(false)
    w.unmount()
  })

  it('downloads the complete daily aggregation for the returned range', async () => {
    const createURL = vi.fn(() => 'blob:daily-report'), revokeURL = vi.fn()
    vi.stubGlobal('URL', { createObjectURL: createURL, revokeObjectURL: revokeURL })
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(() => {})
    const { wrapper: w } = await setup()
    await w.get('[data-testid="report-export"]').trigger('click')
    expect(createURL).toHaveBeenCalledOnce()
    expect(click).toHaveBeenCalledOnce()
    expect(revokeURL).toHaveBeenCalledWith('blob:daily-report')
    expect(document.querySelector('a[download]')).toBeNull()
    w.unmount()
    vi.unstubAllGlobals()
  })
})
