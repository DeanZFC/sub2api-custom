import type { TestSchedule, TestSchedulePeriod } from '@/types'

export const defaultTestSchedule = (): TestSchedule => ({
  timezone: 'Asia/Shanghai',
  periods: [
    { start: '08:00', end: '20:00', interval_minutes: 60 },
    { start: '20:00', end: '08:00', interval_minutes: 180 },
  ],
})

export const copyTestSchedule = (schedule?: TestSchedule | null): TestSchedule | null => schedule
  ? { timezone: schedule.timezone, periods: schedule.periods.map(period => ({ ...period })) } : null

export function testScheduleMinute(value: string, allowEnd = false): number | null {
  if (allowEnd && value === '24:00') return 1440
  if (!/^(?:[01]\d|2[0-3]):[0-5]\d$/.test(value)) return null
  return Number(value.slice(0, 2)) * 60 + Number(value.slice(3))
}

export function testScheduleError(schedule: TestSchedule): string | null {
  try {
    if (!schedule.timezone?.trim() || schedule.timezone === 'Local') return 'timezoneError'
    new Intl.DateTimeFormat('en', { timeZone: schedule.timezone }).format()
  } catch { return 'timezoneError' }
  if (!schedule.periods.length || schedule.periods.length > 24) return 'periodCountError'
  const occupied = new Set<number>()
  for (const period of schedule.periods) {
    const start = testScheduleMinute(period.start)
    const end = testScheduleMinute(period.end, true)
    if (start === null || end === null || start === end) return 'timeError'
    const duration = end > start ? end - start : end - start + 1440
    if (!Number.isInteger(period.interval_minutes) || period.interval_minutes < 1 || period.interval_minutes > duration) return 'intervalError'
    for (let minute = 0; minute < duration; minute++) {
      const index = (start + minute) % 1440
      if (occupied.has(index)) return 'overlapError'
      occupied.add(index)
    }
  }
  return null
}

export const validTestSchedule = (schedule?: TestSchedule | null, cron?: string): boolean => schedule
  ? testScheduleError(schedule) === null : Boolean(cron?.trim())

export function testScheduleTimes(period: TestSchedulePeriod): string[] {
  const start = testScheduleMinute(period.start)
  const end = testScheduleMinute(period.end, true)
  if (start === null || end === null || start === end || !Number.isInteger(period.interval_minutes) || period.interval_minutes < 1) return []
  const duration = end > start ? end - start : end - start + 1440
  if (period.interval_minutes > duration) return []
  const times: string[] = []
  for (let elapsed = 0; elapsed < duration; elapsed += period.interval_minutes) {
    const minute = (start + elapsed) % 1440
    times.push(`${String(Math.floor(minute / 60)).padStart(2, '0')}:${String(minute % 60).padStart(2, '0')}`)
  }
  return times
}
