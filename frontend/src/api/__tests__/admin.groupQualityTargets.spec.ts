import { beforeEach, describe, expect, it, vi } from 'vitest'
import { passiveQualityAPI, groupQualityTargetDraft, defaultPassivePolicy } from '../admin/passiveQuality'
const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('../client', () => ({ default: client }))
beforeEach(() => { vi.clearAllMocks(); client.get.mockResolvedValue({ data: [] }); client.post.mockResolvedValue({ data: {} }); client.put.mockResolvedValue({ data: {} }) })
describe('group quality target API', () => {
  it('loads production group metrics from the summary endpoint', async () => {
    await passiveQualityAPI.groupTargets()
    expect(client.get).toHaveBeenCalledWith('/admin/passive-quality/group-targets')
  })
  it('creates and updates purpose-specific payloads without reuse-protection controls', async () => {
    const draft = groupQualityTargetDraft(undefined, 'GPT-Plus'); draft.group_ids = [4]
    await passiveQualityAPI.saveGroupQualityTarget(draft)
    expect(client.post).toHaveBeenCalledWith('/admin/passive-quality-policies', expect.objectContaining({ purpose: 'group_quality_target', group_ids: [4] }))
    const updated = groupQualityTargetDraft({ ...defaultPassivePolicy(), id: 7, version: 4, purpose: 'group_quality_target', group_ids: [4], excluded_account_ids: [2] })
    await passiveQualityAPI.saveGroupQualityTarget(updated)
    expect(client.put).toHaveBeenCalledWith('/admin/passive-quality-policies/7', expect.objectContaining({ id: 7, version: 4, excluded_account_ids: [2] }))
    expect(client.put.mock.calls[0][1]).not.toHaveProperty('cache_protection')
    expect(client.put.mock.calls[0][1]).not.toHaveProperty('ttft_action')
  })
})
