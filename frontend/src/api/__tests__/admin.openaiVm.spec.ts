import { beforeEach, describe, expect, it, vi } from 'vitest'

const { get, put, post, remove } = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), post: vi.fn(), remove: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { get, put, post, delete: remove } }))
import { openaiVmAPI } from '@/api/admin/openaiVm'

describe('OpenAI dedicated slot API', () => {
  const options = { timeout: 120_000 }
  beforeEach(() => {
    for (const fn of [get, put, post, remove]) fn.mockReset().mockResolvedValue({ data: {} })
  })

  it('reads and saves global configuration through the admin API', async () => {
    const config = { enabled: true, base_url: 'https://bridge.example', proxy_url: 'https://proxy.example', secret_configured: true }
    get.mockResolvedValueOnce({ data: config })
    expect(await openaiVmAPI.getConfig()).toEqual(config)
    expect(get).toHaveBeenCalledWith('/admin/openai-vm/config', options)
    const input = { enabled: true, base_url: config.base_url, proxy_url: config.proxy_url, secret: '' }
    await openaiVmAPI.saveConfig(input)
    expect(put).toHaveBeenCalledWith('/admin/openai-vm/config', input, options)
  })

  it('tests bridge health without account credentials or provisioning payloads', async () => {
    post.mockResolvedValueOnce({ data: { healthy: true } })
    expect(await openaiVmAPI.testConnection()).toEqual({ healthy: true })
    expect(post).toHaveBeenCalledWith('/admin/openai-vm/test', undefined, options)
  })

  it('uses account-scoped endpoints for binding, creation, retry and cleanup', async () => {
    await openaiVmAPI.getBinding(42)
    await openaiVmAPI.createBinding(42, 'kvm')
    await openaiVmAPI.retryBinding(42)
    await openaiVmAPI.deleteBinding(42)
    expect(get).toHaveBeenCalledWith('/admin/accounts/42/vm', options)
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/vm', { runtime_type: 'kvm' }, options)
    expect(post).toHaveBeenCalledWith('/admin/accounts/42/vm/retry', undefined, options)
    expect(remove).toHaveBeenCalledWith('/admin/accounts/42/vm', options)
  })

  it('propagates creation and deletion failures instead of returning success', async () => {
    post.mockRejectedValueOnce(new Error('provisioning timeout'))
    remove.mockRejectedValueOnce(new Error('cleanup failed'))
    await expect(openaiVmAPI.createBinding(42, 'docker')).rejects.toThrow('provisioning timeout')
    await expect(openaiVmAPI.deleteBinding(42)).rejects.toThrow('cleanup failed')
  })
})
