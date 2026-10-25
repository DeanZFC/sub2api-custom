import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put } }))

import { qqbotAPI } from '@/api/qqbot'

describe('QQ bot config version headers', () => {
  beforeEach(() => {
    get.mockReset()
    put.mockReset()
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
