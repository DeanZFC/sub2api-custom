import { beforeEach, describe, expect, it, vi } from 'vitest'

const mocks = vi.hoisted(() => ({ get: vi.fn(), put: vi.fn() }))
vi.mock('../client', () => ({ apiClient: { get: mocks.get, put: mocks.put } }))
const { get, put } = mocks

import {
  listSharedAccounts,
  setSharedAccountStatus,
  setSharedAccountListed,
  listSharedUsers,
  setSharedUserPublishPermission
} from '../admin/sharedPool'

describe('admin shared pool API', () => {
  beforeEach(() => { get.mockReset(); put.mockReset() })

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
})
