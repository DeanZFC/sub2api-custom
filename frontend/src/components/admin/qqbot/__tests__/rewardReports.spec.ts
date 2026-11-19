import { describe, expect, it } from 'vitest'
import type { QQRewardReport } from '@/api/qqbot'
import { rewardReportAmount, rewardReportDates, rewardReportDailyCSV, validRewardReportDates } from '../rewardReports'

describe('QQ reward report dates and exported totals', () => {
  it('uses the Beijing date across UTC midnight and calendar boundaries', () => {
    const before = new Date('2026-11-30T15:59:59Z')
    const after = new Date('2026-11-30T16:00:00Z')
    expect(rewardReportDates('today', before)).toEqual({ start_date: '2026-11-30', end_date: '2026-11-30' })
    expect(rewardReportDates('today', after)).toEqual({ start_date: '2026-12-01', end_date: '2026-12-01' })
    expect(rewardReportDates('yesterday', after)).toEqual({ start_date: '2026-11-30', end_date: '2026-11-30' })
    expect(rewardReportDates('last7', after)).toEqual({ start_date: '2026-11-25', end_date: '2026-12-01' })
    expect(rewardReportDates('last30', after)).toEqual({ start_date: '2026-11-02', end_date: '2026-12-01' })
    expect(rewardReportDates('month', after)).toEqual({ start_date: '2026-12-01', end_date: '2026-12-01' })
  })

  it('validates real calendar dates and the inclusive 366-day limit', () => {
    expect(validRewardReportDates('2024-01-01', '2024-12-31')).toBe(true)
    expect(validRewardReportDates('2024-01-01', '2025-01-01')).toBe(false)
    expect(validRewardReportDates('2026-02-29', '2026-03-01')).toBe(false)
    expect(validRewardReportDates('2026-11-19', '2026-11-18')).toBe(false)
    expect(validRewardReportDates('', '2026-11-18')).toBe(false)
  })

  it('preserves small balances and exports all days independently of detail pagination', () => {
    expect(rewardReportAmount(0.00000001)).toBe('0.00000001')
    expect(rewardReportAmount(1.23000000)).toBe('1.23')
    expect(rewardReportAmount(0)).toBe('0')
    const row = { reward_count: 1, user_count: 1, balance_amount: 0.00000001, chances_quantity: 2, coupon_quantity: 3, pending_quantity: 4, fulfilled_quantity: 5 }
    const report = { daily: [{ date: '2026-11-17', ...row }, { date: '2026-11-18', ...row }], items: [], total: 500, page: 2 } as unknown as QQRewardReport
    const csv = rewardReportDailyCSV(report, ['Date', 'Balance "USD"', 'Users', 'Entries', 'Chances', 'Coupons', 'Pending', 'Delivered'])
    expect(csv).toContain('"Balance ""USD"""')
    expect(csv).toContain('"2026-11-17","0.00000001","1","1","2","3","4","5"')
    expect(csv).toContain('"2026-11-18","0.00000001"')
    expect(csv.charCodeAt(0)).toBe(0xfeff)
  })
})
