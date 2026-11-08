import { afterEach, describe, expect, it, vi } from 'vitest'
import { apiClient } from '../client'
import { getCardMatrices, getMatrix, repeatedArrayParamsSerializer } from '../channelMonitorV2'

afterEach(() => vi.restoreAllMocks())

describe('channel monitor V2 query serialization', () => {
  it.each([
    ['platform_group', 'platform_group_model'],
    ['platform', 'platform_model'],
  ] as const)('loads %s totals and %s details with identical filters and viewer scope', async (groupBy, modelGroupBy) => {
    const total = { items: [{ metrics: { cache_rate: 0.8 } }] }
    const models = { items: [{ model: 'gpt-test', metrics: { cache_rate: 0.4 } }] }
    const get = vi.spyOn(apiClient, 'get').mockResolvedValueOnce({ data: total }).mockResolvedValueOnce({ data: models })
    const controller = new AbortController()
    const result = await getCardMatrices({ range: '24h', platforms: ['openai'], groupIds: [7], models: ['gpt-test'] }, groupBy, true, controller.signal)
    expect(result.matrix).toBe(total)
    expect(result.modelMatrix).toBe(models)
    expect(get).toHaveBeenCalledTimes(2)
    for (const [index, grouping] of [groupBy, modelGroupBy].entries()) {
      expect(get).toHaveBeenNthCalledWith(index + 1, '/admin/channel-monitor-v2/matrix', expect.objectContaining({
        signal: controller.signal,
        params: { range: '24h', platform: ['openai'], group_id: [7], model: ['gpt-test'], group_by: grouping, include_recent_requests: true },
      }))
    }
  })

  it('does not load duplicate model detail for an existing model view', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { items: [] } })
    const result = await getCardMatrices({ range: '90m', platforms: [], groupIds: [], models: [] }, 'platform_group_model')
    expect(get).toHaveBeenCalledTimes(1)
    expect(result.modelMatrix).toBeNull()
  })

  it('opts into recent requests only for the channel-card matrix', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({ data: { items: [] } })
    const controller = new AbortController()
    await getMatrix({ range: '90m', platforms: [], groupIds: [7], models: ['gpt-test'] }, 'platform_group', false, controller.signal, true)
    expect(get).toHaveBeenCalledWith('/channel-monitor-v2/matrix', expect.objectContaining({
      params: expect.objectContaining({ include_recent_requests: true, group_id: [7], model: ['gpt-test'] }),
      signal: controller.signal,
    }))
  })
  it('uses repeated keys without bracket suffixes for array filters', () => {
    const query = repeatedArrayParamsSerializer({
      range: '90m',
      platform: ['openai', 'grok'],
      group_id: [1, 2],
      model: undefined,
      group_by: 'platform_group_model',
    })

    expect(query).toBe('range=90m&platform=openai&platform=grok&group_id=1&group_id=2&group_by=platform_group_model')
    expect(query).not.toContain('%5B%5D')
  })

  it('sends the matrix grouping with the shared filters', async () => {
    const get = vi.spyOn(apiClient, 'get').mockResolvedValue({
      data: { coverage: {}, group_by: 'platform_group', items: [] },
    })

    await getMatrix({ range: '24h', platforms: ['openai'], groupIds: [7], models: [] }, 'platform_group', true)

    expect(get).toHaveBeenCalledWith('/admin/channel-monitor-v2/matrix', expect.objectContaining({
      params: {
        range: '24h',
        platform: ['openai'],
        group_id: [7],
        model: undefined,
        group_by: 'platform_group',
      },
    }))
  })
})
