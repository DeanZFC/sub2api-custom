import { beforeEach, describe, expect, it, vi } from 'vitest'
import { passiveQualityAPI, type PassiveState } from '../admin/passiveQuality'
const client = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('../client', () => ({ default: client }))
beforeEach(() => { vi.clearAllMocks(); client.post.mockResolvedValue({ data: {} }) })
describe('model-specific passive quality actions', () => {
  it('passes the exact model for verification, exemption and cancel', async () => {
    const state = { policy_id: 4, account_id: 62, scope: 'model', model: 'model-b' } as PassiveState
    for (const action of ['verify', 'exempt', 'cancel_exemption']) {
      await passiveQualityAPI.act(state, action, action === 'exempt' ? 600 : 0)
      expect(client.post).toHaveBeenLastCalledWith('/admin/passive-quality-policies/4/accounts/62/action', { action, seconds: action === 'exempt' ? 600 : 0, model: 'model-b' })
    }
  })
  it('retains the existing account-wide action payload when model is empty', async () => {
    await passiveQualityAPI.act({ policy_id: 4, account_id: 62, scope: 'account', model: '' } as PassiveState, 'verify')
    expect(client.post).toHaveBeenCalledWith('/admin/passive-quality-policies/4/accounts/62/action', { action: 'verify', seconds: 0 })
  })
})
