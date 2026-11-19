import type { QQRewardReport } from '@/api/qqbot'

export type RewardReportPreset = 'today' | 'yesterday' | 'last7' | 'last30' | 'month'

// Calendar arithmetic uses UTC after selecting the Beijing date, avoiding the
// browser's timezone and daylight-saving transitions.
export function rewardReportDates(preset: RewardReportPreset, now = new Date()) {
  const today = new Date(now.getTime() + 8 * 3600000).toISOString().slice(0, 10)
  const end = new Date(`${today}T00:00:00Z`)
  if (preset === 'yesterday') end.setUTCDate(end.getUTCDate() - 1)
  const start = new Date(end)
  if (preset === 'last7') start.setUTCDate(start.getUTCDate() - 6)
  if (preset === 'last30') start.setUTCDate(start.getUTCDate() - 29)
  if (preset === 'month') start.setUTCDate(1)
  return { start_date: start.toISOString().slice(0, 10), end_date: end.toISOString().slice(0, 10) }
}

export function validRewardReportDates(start: string, end: string) {
  const values = [start, end].map(value => /^\d{4}-\d{2}-\d{2}$/.test(value) ? Date.parse(`${value}T00:00:00Z`) : NaN)
  if (values.some((value, index) => !Number.isFinite(value) || new Date(value).toISOString().slice(0, 10) !== [start, end][index])) return false
  const days = (values[1]! - values[0]!) / 86400000 + 1
  return days >= 1 && days <= 366
}

export function rewardReportAmount(amount: number) {
  return amount.toFixed(8).replace(/0+$/, '').replace(/\.$/, '')
}

// Export the complete daily aggregation returned for the selected period,
// independent of the current page of individual rewards.
export function rewardReportDailyCSV(report: QQRewardReport, headers: string[]) {
  const rows: (string | number)[][] = [headers, ...report.daily.map(row => [row.date, rewardReportAmount(row.balance_amount), row.user_count, row.reward_count, row.chances_quantity, row.coupon_quantity, row.pending_quantity, row.fulfilled_quantity])]
  return '\uFEFF' + rows.map(row => row.map(value => `"${String(value).replace(/"/g, '""')}"`).join(',')).join('\r\n') + '\r\n'
}
