import { describe, expect, it } from 'vitest'
import { defaultAutonomousConfig } from '@/api/qqbot'
import { enableAutonomousChat } from '../autonomousDefaults'
describe('one-switch auto chat', () => {
 it('inherits allowed groups without an extra picker', () => {
  const c = defaultAutonomousConfig()
  expect(enableAutonomousChat(c)).toMatchObject({ enabled: true, inherit_groups: true })
  expect(c.enabled).toBe(false)
 })
 it('preserves explicit custom scope, zero limits and hours', () => {
  const c = { ...defaultAutonomousConfig(), groups: ['group'], inherit_groups: false, daily_calls: 0, start_hour: 9 }
  expect(enableAutonomousChat(c)).toEqual({ ...c, enabled: true })
 })
 it('upgrades untouched legacy defaults only on opt-in', () => {
  const c = { ...defaultAutonomousConfig(), sample_percent: 15, cooldown_seconds: 120, min_messages: 6, member_cooldown_seconds: 600, hourly_replies: 3, daily_replies: 20, daily_calls: 40, start_hour: 9, end_hour: 23, followup_seconds: 60 }
  expect(enableAutonomousChat(c)).toMatchObject({ min_messages: 2, followup_seconds: 90, inherit_groups: true })
  expect(enableAutonomousChat({ ...c, daily_calls: 0 }).daily_calls).toBe(0)
  expect(enableAutonomousChat({ ...c, enabled: true }).min_messages).toBe(6)
 })
})
