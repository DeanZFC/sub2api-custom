import { beforeEach, describe, expect, it, vi } from 'vitest'
const { get } = vi.hoisted(() => ({ get: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get } }))
import { getCodexTicketHistory } from '@/api/admin/accounts'

describe('Codex ticket diagnostics API', () => {
  beforeEach(() => { get.mockReset() })

  it('scopes history by account and model and supports cancellation', async () => {
    const data = { events: [], limit: 20 }
    const controller = new AbortController()
    get.mockResolvedValueOnce({ data })
    await expect(getCodexTicketHistory(17, 'gpt-6-astra', controller.signal)).resolves.toEqual(data)
    expect(get).toHaveBeenCalledWith('/admin/accounts/17/codex-ticket-history', {
      params: { model: 'gpt-6-astra' }, signal: controller.signal,
    })
  })

})
