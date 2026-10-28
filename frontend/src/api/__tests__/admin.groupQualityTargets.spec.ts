import { defaultPassivePolicy } from '@/test/fixtures/passiveQuality'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import { passiveQualityAPI, groupQualityTargetDraft } from '../admin/passiveQuality'
const client = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), delete: vi.fn() }))
vi.mock('../client', () => ({ default: client }))
beforeEach(() => { vi.clearAllMocks(); client.get.mockResolvedValue({ data: [] }); client.post.mockResolvedValue({ data: {} }); client.put.mockResolvedValue({ data: {} }); client.delete.mockResolvedValue({}) })
describe('group quality target API', () => {
  it('loads production group metrics from the summary endpoint', async () => {
    await passiveQualityAPI.groupTargets()
    expect(client.get).toHaveBeenCalledWith('/admin/passive-quality/group-targets')
  })
  it('deletes a target through the policy endpoint', async () => {
    await passiveQualityAPI.remove(11)
    expect(client.delete).toHaveBeenCalledWith('/admin/passive-quality-policies/11')
  })
  it('uses unified defaults without overwriting existing first-token settings', () => {
    expect(groupQualityTargetDraft(undefined, ' GPT-Pro ').ttft).toEqual({ enabled: true, pause: 15000, recover: 12000 })
    expect(groupQualityTargetDraft(undefined, 'Gpt-Plus').ttft).toEqual({ enabled: true, pause: 15000, recover: 12000 })
    const saved = { ...defaultPassivePolicy(), ttft: { enabled: true, pause: 18000, recover: 14000 } }
    expect(groupQualityTargetDraft(saved, 'GPT-Pro').ttft).toEqual(saved.ttft)
    expect(groupQualityTargetDraft(undefined, 'GPT-Pro').cache_rate.pause).toBe(80)
    expect(groupQualityTargetDraft(undefined, 'GPT-Pro').error_rate.pause).toBe(10)
    expect(groupQualityTargetDraft(undefined, 'GPT-Pro').tps).toEqual({ enabled: true, pause: 30, recover: 36 })
    expect(groupQualityTargetDraft(undefined, 'GPT-Pro').traffic_mode).toBe('strict')
    expect(groupQualityTargetDraft().stability).toEqual({ availability_pause_seconds: 60, performance_pause_seconds: 300, recovery_seconds: 120, new_session_percent: 20 })
    expect(groupQualityTargetDraft({ ...saved, traffic_mode: 'availability' }, 'GPT-Pro').traffic_mode).toBe('availability')
  })
  it('preserves explicit stability settings including zero duration and sends them on save', async () => {
    const stability = { availability_pause_seconds: 0, performance_pause_seconds: 600, recovery_seconds: 180, new_session_percent: 30 }
    const draft = groupQualityTargetDraft({ ...defaultPassivePolicy(), purpose: 'group_quality_target', stability })
    expect(draft.stability).toEqual(stability)
    expect(draft.stability).not.toBe(stability)
    await passiveQualityAPI.saveGroupQualityTarget(draft)
    expect(client.post).toHaveBeenCalledWith('/admin/passive-quality-policies', expect.objectContaining({ stability }))
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
