import { defaultAutonomousConfig, type QQAutonomousConfig } from '@/api/qqbot'

// Upgrade only the untouched legacy profile on opt-in. Custom scopes, zero
// budgets and other operator overrides remain authoritative.
const legacy = { sample_percent: 15, cooldown_seconds: 120, min_messages: 6,
  member_cooldown_seconds: 600, hourly_replies: 3, daily_replies: 20, daily_calls: 40,
  start_hour: 9, end_hour: 23, followup_seconds: 60, max_chars: 100, mention_user: false }
export function enableAutonomousChat(current: QQAutonomousConfig): QQAutonomousConfig {
  const next = { ...current, enabled: true }
  if (!current.enabled && Object.entries(legacy).every(([key, value]) => current[key as keyof QQAutonomousConfig] === value)) {
    const defaults = defaultAutonomousConfig()
    for (const key of Object.keys(legacy) as (keyof typeof legacy)[]) Object.assign(next, { [key]: defaults[key] })
  }
  if (!next.groups.length) next.inherit_groups = true
  return next
}
