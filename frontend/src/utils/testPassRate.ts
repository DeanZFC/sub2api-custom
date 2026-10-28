import type { TestPassRateConfig, TestSchedule } from '@/types'
import { testScheduleError, testScheduleTimes } from './testSchedule'

export const supportsTestPassRate = (kind: string): boolean =>
  ['number', 'text', 'html', 'svg', 'model_check', 'capability'].includes(kind)

export const defaultTestPassRate = (): TestPassRateConfig => ({
  enabled: true, window_size: 10, window_seconds: 604800,
  pause_below: 80, recover_at_least: 90, recover_consecutive: 2,
  pause_on_single_failure: false,
})

export function validTestPassRate(config: TestPassRateConfig | undefined, kind: string): boolean {
  if (!config) return true
  if (typeof config.enabled !== 'boolean') return false
  if (!config.enabled) return true
  const integer = (value: number, min: number, max: number) => Number.isInteger(value) && value >= min && value <= max
  return supportsTestPassRate(kind)
    && integer(config.window_size, 1, 1000)
    && integer(config.window_seconds, 3600, 2592000)
    && Number.isFinite(config.pause_below) && config.pause_below >= 0 && config.pause_below <= 100
    && Number.isFinite(config.recover_at_least) && config.recover_at_least >= config.pause_below && config.recover_at_least <= 100
    && integer(config.recover_consecutive, 1, config.window_size)
    && typeof config.pause_on_single_failure === 'boolean'
}

/** Upper bound for a daily schedule; detects impossible windows without promising usable samples. */
export function passRateScheduleCapacity(config: TestPassRateConfig, schedule?: TestSchedule | null): number | null {
  if (!schedule || testScheduleError(schedule) || !Number.isFinite(config.window_seconds) || config.window_seconds <= 0) return null
  const times = schedule.periods.flatMap(testScheduleTimes)
  // Include an extra local day for an inclusive cutoff and daylight-saving changes.
  // An optimistic bound avoids falsely marking a fillable window as impossible.
  return times.length * (Math.ceil(config.window_seconds / 86400) + 1)
}
