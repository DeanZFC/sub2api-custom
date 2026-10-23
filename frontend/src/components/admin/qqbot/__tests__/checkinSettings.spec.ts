import { beforeEach, describe, expect, it, vi } from 'vitest'
import { saveCheckinSettings } from '../checkinSettings'
import type { QQCheckinConfig } from '@/api/qqbot'
const api = vi.hoisted(() => ({ checkinConfig: vi.fn(), saveCheckin: vi.fn() }))
vi.mock('@/api/qqbot', () => ({ qqbotAPI: api }))

describe('check-in draft revision protection', () => {
  const draft = () => ({ enabled: true, min_amount: 0.1, max_amount: 0.2, min_recharge_total: 0, min_recharge_30d: 0, recharge_window_days: 30, streak_days: 7, streak_chances: 0, streak_reward: { type: 'chances', amount: 0, quantity: 0, prize_name: '', claim_instructions: '' }, growth: { tasks: [], periodic_tasks: [] } }) as QQCheckinConfig
  beforeEach(() => {
    vi.resetAllMocks()
    api.checkinConfig.mockResolvedValue({ ...draft(), _version: 'fresh-whole-config', _sections: { checkin: 'fresh-checkin' } })
    api.saveCheckin.mockImplementation(async value => value)
  })
  it('keeps the original section revision while refreshing unrelated fields', async () => {
    await saveCheckinSettings({ ...draft(), _version: 'old-whole-config', _sections: { checkin: 'old-checkin' } })
    expect(api.saveCheckin).toHaveBeenCalledWith(expect.objectContaining({ _version: 'old-checkin', _section: 'checkin' }))
  })
  it('never upgrades an unversioned draft to the freshly fetched revision', async () => {
    await saveCheckinSettings(draft())
    expect(api.saveCheckin.mock.calls[0]![0]._version).toBeUndefined()
    expect(api.saveCheckin.mock.calls[0]![0]._section).toBeUndefined()
  })
})
