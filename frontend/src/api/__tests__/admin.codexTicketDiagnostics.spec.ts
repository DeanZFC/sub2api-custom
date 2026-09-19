import { beforeEach, describe, expect, it, vi } from 'vitest'
const { get, post } = vi.hoisted(() => ({ get: vi.fn(), post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, post } }))
import { getCodexTicketHistory } from '@/api/admin/accounts'
import { testCodexTicketProxy } from '@/api/admin/settings'

describe('Codex ticket diagnostics API', () => {
  beforeEach(() => { get.mockReset(); post.mockReset() })

  it('scopes history by account and model and supports cancellation', async () => {
    const data = { events: [], limit: 20 }
    const controller = new AbortController()
    get.mockResolvedValueOnce({ data })
    await expect(getCodexTicketHistory(17, 'gpt-6-astra', controller.signal)).resolves.toEqual(data)
    expect(get).toHaveBeenCalledWith('/admin/accounts/17/codex-ticket-history', {
      params: { model: 'gpt-6-astra' }, signal: controller.signal,
    })
  })

  it('sends only the saved proxy index to exit testing', async () => {
    const data = { proxy_index: 2, success: false, latency_ms: 90, error_code: 'proxy_auth' }
    const controller = new AbortController()
    post.mockResolvedValueOnce({ data })
    await expect(testCodexTicketProxy(2, controller.signal)).resolves.toEqual(data)
    expect(post).toHaveBeenCalledWith('/admin/settings/openai-codex-ticket/test-proxy', { proxy_index: 2 }, { signal: controller.signal })
  })
})
