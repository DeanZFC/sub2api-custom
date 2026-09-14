import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn(), delete: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get: mocks.get, put: mocks.put, delete: mocks.delete } }))
const { get, put, delete: del } = mocks

import {
  listSharedAccounts,
  setSharedAccountStatus,
  setSharedAccountListed,
  deleteSharedAccount,
  listSharedUsers,
  setSharedUserPublishPermission,
  listSharedRevenue,
  listSharedRevenueRecords
} from '../admin/sharedPool'

describe('admin shared pool API', () => {
  beforeEach(() => { get.mockReset(); put.mockReset(); del.mockReset() })

  it('uses isolated listing endpoints for account controls', async () => {
    get.mockResolvedValue({ data: { items: [] } })
    put.mockResolvedValue({ data: {} })
    await listSharedAccounts({ status: 'suspended' })
    await setSharedAccountStatus(4, 'suspended')
    await setSharedAccountListed(4, false)
    expect(get).toHaveBeenCalledWith('/admin/shared-pool/listings', { params: { status: 'suspended' } })
    expect(put).toHaveBeenNthCalledWith(1, '/admin/shared-pool/listings/4/status', { status: 'suspended' })
    expect(put).toHaveBeenNthCalledWith(2, '/admin/shared-pool/listings/4/listed', { listed: false })
  })

  it('deletes only through the isolated shared-pool admin endpoint', async () => {
    del.mockResolvedValue({ data: {} })
    await deleteSharedAccount(12)
    expect(del).toHaveBeenCalledWith('/admin/shared-pool/listings/12')
  })

  it('updates a users shared publishing permission independently', async () => {
    get.mockResolvedValue({ data: { items: [] } })
    put.mockResolvedValue({ data: {} })
    await listSharedUsers({ search: 'alice' })
    await setSharedUserPublishPermission(9, false, '多次失败', '2026-09-20T00:00:00Z')
    expect(get).toHaveBeenCalledWith('/admin/shared-pool/users', { params: { search: 'alice' } })
    expect(put).toHaveBeenCalledWith('/admin/shared-pool/users/9/publish-permission', {
      enabled: false,
      reason: '多次失败',
      blocked_until: '2026-09-20T00:00:00Z'
    })
  })

  it('passes publish permission filters and pagination to the admin users endpoint', async () => {
    get.mockResolvedValue({ data: { items: [], total: 42, page: 2, page_size: 20 } })
    const result = await listSharedUsers({ search: 'alice', publish_enabled: 'false', page: 2, page_size: 20 })
    expect(get).toHaveBeenCalledWith('/admin/shared-pool/users', {
      params: { search: 'alice', publish_enabled: 'false', page: 2, page_size: 20 }
    })
    expect(result.total).toBe(42)
    expect(result.page).toBe(2)
  })

  it('keeps admin earnings and request records on dedicated shared-pool endpoints', async () => {
    get.mockResolvedValue({ data: { items: [], total: 0, page: 1, page_size: 20 } })
    await listSharedRevenue({ search: 'alice', page: 1, page_size: 20 })
    await listSharedRevenueRecords({ owner_id: 9, model: 'gpt-5.4', page: 2, page_size: 10 })
    expect(get).toHaveBeenNthCalledWith(1, '/admin/shared-pool/revenue', { params: { search: 'alice', page: 1, page_size: 20 } })
    expect(get).toHaveBeenNthCalledWith(2, '/admin/shared-pool/revenue/records', { params: { owner_id: 9, model: 'gpt-5.4', page: 2, page_size: 10 } })
  })
})
