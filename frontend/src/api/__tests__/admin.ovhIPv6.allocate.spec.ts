import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

const { post } = vi.hoisted(() => ({ post: vi.fn() }))
vi.mock('@/api/client', () => ({ apiClient: { post } }))

beforeEach(() => {
  sessionStorage.clear()
  vi.resetModules()
  post.mockReset().mockResolvedValue({ data: { id: 43, managed_ipv6: '2001:db8::43', managed_ipv6_state: 'reserved' } })
})
afterEach(() => vi.restoreAllMocks())

describe('reserved IPv6 allocation HTTP contract', () => {
  it('sends an idempotency header with the normalized reservation request', async () => {
    vi.spyOn(globalThis.crypto, 'randomUUID').mockReturnValue('11111111-1111-4111-8111-111111111111')
    const { ovhIPv6API } = await import('@/api/admin/ovhIPv6')
    const proxy = await ovhIPv6API.allocate('  my reservation  ')
    expect(post).toHaveBeenCalledWith('/admin/proxies/ovh-ipv6/allocate', { name: 'my reservation' }, {
      headers: { 'Idempotency-Key': 'ovh-ipv6-allocate-11111111-1111-4111-8111-111111111111' }
    })
    expect(proxy).toMatchObject({ id: 43, managed_ipv6_state: 'reserved' })
    expect(sessionStorage.length).toBe(0)
  })

  it('uses the same operation after an uncertain timeout, including whitespace normalization', async () => {
    const { ovhIPv6API } = await import('@/api/admin/ovhIPv6')
    post.mockRejectedValueOnce(new Error('network timeout'))
    await expect(ovhIPv6API.allocate('  pending  ')).rejects.toThrow('network timeout')
    const firstHeaders = post.mock.calls[0]![2].headers
    await ovhIPv6API.allocate('pending')
    expect(post.mock.calls[1]![2].headers).toEqual(firstHeaders)
    expect(sessionStorage.length).toBe(0)
  })

  it('retains an unnamed pending operation across reloads', async () => {
    const { ovhIPv6API } = await import('@/api/admin/ovhIPv6')
    post.mockRejectedValueOnce(new Error('connection closed'))
    await expect(ovhIPv6API.allocate()).rejects.toThrow('connection closed')
    const firstHeaders = post.mock.calls[0]![2].headers
    vi.resetModules()
    const reloaded = await import('@/api/admin/ovhIPv6')
    await reloaded.ovhIPv6API.allocate(' ')
    expect(post.mock.calls[1]![2].headers).toEqual(firstHeaders)
    expect(post.mock.calls[1]![1]).toEqual({ name: undefined })
    expect(sessionStorage.length).toBe(0)
  })

  it('starts a fresh operation after confirmed success even if the name is unchanged', async () => {
    const { ovhIPv6API } = await import('@/api/admin/ovhIPv6')
    await ovhIPv6API.allocate('same name')
    const firstKey = post.mock.calls[0]![2].headers['Idempotency-Key']
    await ovhIPv6API.allocate('same name')
    expect(post.mock.calls[1]![2].headers['Idempotency-Key']).not.toBe(firstKey)
  })

  it('does not reuse the key for a changed payload', async () => {
    const { ovhIPv6API } = await import('@/api/admin/ovhIPv6')
    post.mockRejectedValueOnce(new Error('timeout'))
    await expect(ovhIPv6API.allocate('first')).rejects.toThrow('timeout')
    await ovhIPv6API.allocate('second')
    expect(post.mock.calls[1]![2].headers).not.toEqual(post.mock.calls[0]![2].headers)
    await ovhIPv6API.allocate('first')
    expect(post.mock.calls[2]![2].headers).toEqual(post.mock.calls[0]![2].headers)
  })

  it('retains retry protection when session storage is blocked', async () => {
    vi.spyOn(Storage.prototype, 'getItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'setItem').mockImplementation(() => { throw new Error('blocked') })
    vi.spyOn(Storage.prototype, 'removeItem').mockImplementation(() => { throw new Error('blocked') })
    const { ovhIPv6API } = await import('@/api/admin/ovhIPv6')
    post.mockRejectedValueOnce(new Error('timeout'))
    await expect(ovhIPv6API.allocate()).rejects.toThrow('timeout')
    await ovhIPv6API.allocate()
    expect(post.mock.calls[1]![2].headers).toEqual(post.mock.calls[0]![2].headers)
  })
})
