import { describe, expect, it } from 'vitest'
import type { TestPassRateConfig, TestProtectionConfig, TestSchedule } from '@/types'
import { defaultTestPassRate, passRateScheduleCapacity, supportsTestPassRate, validTestPassRate } from '../testPassRate'
import { copyTestProtection, normalizeAllPassProtection, validTestProtection } from '../testProtection'

describe('active check pass-rate settings', () => {
  it('supports active verdicts and excludes passive statistics', () => {
    for (const kind of ['number', 'text', 'html', 'svg', 'model_check', 'capability']) {
      expect(supportsTestPassRate(kind)).toBe(true)
      expect(validTestPassRate(defaultTestPassRate(), kind)).toBe(true)
    }
    expect(validTestPassRate(defaultTestPassRate(), 'statistics')).toBe(false)
    expect(validTestPassRate(undefined, 'statistics')).toBe(true)
    expect(validTestPassRate({ ...defaultTestPassRate(), enabled: false }, 'statistics')).toBe(true)
  })

  it.each<Partial<TestPassRateConfig>>([
    { window_size: 1 }, { window_size: 1001 }, { window_size: 2.5 },
    { window_seconds: 3599 }, { window_seconds: 2592001 },
    { pause_below: -1 }, { pause_below: Number.NaN }, { recover_at_least: 79 },
    { recover_at_least: 101 }, { recover_consecutive: 0 }, { recover_consecutive: 11 },
  ])('rejects invalid boundaries: %j', patch => {
    expect(validTestPassRate({ ...defaultTestPassRate(), ...patch }, 'number')).toBe(false)
  })

  it('preserves rate settings through copies and aggregate normalization without mutating the source', () => {
    const original: TestProtectionConfig = { enabled: true, mode: 'all_pass', rules: [{
      test_definition_id: 1, priority: 3, required_pass: false, expected_answer: '29',
      pause_on_failure: false, pass_rate: defaultTestPassRate(),
    }] }
    const copy = copyTestProtection(original)
    normalizeAllPassProtection(copy)
    expect(copy.rules[0].pass_rate).toEqual(original.rules[0].pass_rate)
    copy.rules[0].pass_rate!.window_size = 20
    expect(original.rules[0].pass_rate!.window_size).toBe(10)
    expect(original.rules[0].pause_on_failure).toBe(false)
    const types = [{ id: 1, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true }]
    expect(validTestProtection(copy, types)).toBe(true)
    copy.rules[0].pass_rate!.recover_at_least = 79
    expect(validTestProtection(copy, types)).toBe(false)
  })

  it('detects schedules unable to fill the window without treating cron-only schedules as known', () => {
    const schedule: TestSchedule = { timezone: 'Asia/Shanghai', periods: [{ start: '08:00', end: '09:00', interval_minutes: 60 }] }
    expect(passRateScheduleCapacity(defaultTestPassRate(), schedule)).toBe(8)
    expect(passRateScheduleCapacity(defaultTestPassRate(), undefined)).toBeNull()
    expect(passRateScheduleCapacity(defaultTestPassRate(), { ...schedule, timezone: 'Invalid' })).toBeNull()
  })

  it('requires a capability verdict rather than timing or execution status alone', () => {
    const types = [{ id: 1, name: 'Candy', key: 'candy', output_kind: 'number', prompt: 'Count', enabled: true }]
    const value: TestProtectionConfig = { enabled: true, rules: [{
      test_definition_id: 1, priority: 0, required_pass: false,
      pause_on_failure: true, pass_rate: defaultTestPassRate(),
      thresholds: [{ metric: 'latency_ms', operator: 'gt', value: 3000 }],
    }] }
    expect(validTestProtection(value, types)).toBe(false)
    value.rules[0].expected_answer = '29'
    expect(validTestProtection(value, types)).toBe(true)
    delete value.rules[0].expected_answer
    value.rules[0].thresholds!.push({ metric: 'output_numeric', operator: 'lt', value: 20 })
    expect(validTestProtection(value, types)).toBe(true)
    value.rules[0].thresholds = []
    value.rules[0].vote = { enabled: true, pass_at_least: 1, reject_above: 0 }
    expect(validTestProtection(value, types)).toBe(true)
    delete value.rules[0].vote
    expect(validTestProtection(value, [{ ...types[0], output_kind: 'capability' }])).toBe(true)
    expect(validTestProtection(value, [{ ...types[0], output_kind: 'model_check' }])).toBe(true)
  })
})
