import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put, post } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put, post } }))

import { qqbotAPI } from '@/api/qqbot'

describe('QQ bot config version headers', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
    post.mockReset()
  })

  it('sends only the scoped question to the dedicated preview endpoint', async () => {
    const value = { text: '真实回答', action: 'chat', input_tokens: 1, output_tokens: 1, duration_ms: 20, knowledge_sources: [] }
    post.mockResolvedValueOnce({ data: value })
    const request = { group_id: 'group', member_id: 'member', question: '接入方式是什么？' }
    expect(await qqbotAPI.chatPreview(request)).toEqual(value)
    expect(post).toHaveBeenCalledOnce()
    expect(post).toHaveBeenCalledWith('/admin/qqbot/chat-preview', request, { timeout: 180000 })
  })

  it('uses the uncompressed config version when a proxy rewrites the ETag', async () => {
    const version = 'a'.repeat(64)
    const config = {
      enabled: true,
      rewards_enabled: false,
      effective_day: '',
      token_rewards: [],
      amount_rewards: [],
    }
    get.mockResolvedValueOnce({
      data: config,
      headers: { etag: `"${version}-gzip"`, 'x-qqbot-config-version': version },
    })
    put.mockResolvedValueOnce({ data: config, headers: { 'x-qqbot-config-version': version } })

    const loaded = await qqbotAPI.rankingConfig()
    expect(loaded._version).toBe(version)

    await qqbotAPI.saveRankings(loaded)
    expect(put).toHaveBeenCalledWith('/admin/qqbot/rankings', loaded, {
      headers: { 'If-Match': version, 'X-QQBot-Section': '' },
    })
  })
})
