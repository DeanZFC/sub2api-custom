import { apiClient } from '../client'

export interface AdminSharedAccount {
  id: number
  account_id?: number
  listing_id?: number
  name?: string
  display_name?: string
  platform: string
  type?: string
  status: string
  listed?: boolean
  uploader_id?: number
  uploader_name?: string
  uploader_email?: string
  sell_rate?: number
  total_call_count?: number
  average_latency_ms?: number
  last_called_at?: string
  recent_calls?: Array<{ duration_ms?: number; result_status?: string }>
  disabled_reason?: string
  created_at?: string
}

export interface AdminSharedUser {
  user_id: number
  email?: string
  username?: string
  publish_enabled: boolean
  block_reason?: string
  blocked_until?: string
  shared_active_account_limit?: number
  shared_account_count?: number
}

export async function listSharedAccounts(params: Record<string, unknown> = {}) {
  const { data } = await apiClient.get<{ items: AdminSharedAccount[]; total?: number }>('/admin/shared-pool/listings', { params })
  return data
}

export async function setSharedAccountStatus(id: number, status: 'active' | 'suspended') {
  const { data } = await apiClient.put<AdminSharedAccount>(`/admin/shared-pool/listings/${id}/status`, { status })
  return data
}

export async function setSharedAccountListed(id: number, listed: boolean) {
  const { data } = await apiClient.put<AdminSharedAccount>(`/admin/shared-pool/listings/${id}/listed`, { listed })
  return data
}
export async function deleteSharedAccount(id: number) {
  await apiClient.delete(`/admin/shared-pool/listings/${id}`)
}

export async function listSharedUsers(params: Record<string, unknown> = {}) {
  const { data } = await apiClient.get<{ items: AdminSharedUser[]; total?: number }>('/admin/shared-pool/users', { params })
  return data
}

export async function setSharedUserPublishPermission(id: number, enabled: boolean, reason?: string, blockedUntil?: string) {
  const { data } = await apiClient.put<AdminSharedUser>(`/admin/shared-pool/users/${id}/publish-permission`, {
    enabled,
    reason,
    blocked_until: blockedUntil || undefined
  })
  return data
}
