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

export async function getSharedAccount(id: number) {
  const { data } = await apiClient.get<import('@/types').Account>(`/admin/shared-pool/listings/${id}/account`)
  return data
}

export async function getSharedAccountModels(id: number) {
  const { data } = await apiClient.get<Array<{ id: string; display_name?: string; type?: string }>>(`/admin/shared-pool/listings/${id}/models`)
  return data
}

export async function updateSharedAccount(id: number, input: Record<string, unknown>) {
  const { data } = await apiClient.put<import('@/types').Account>(`/admin/shared-pool/listings/${id}/account`, input)
  return data
}

export interface AdminSharedUsersPage {
  items: AdminSharedUser[]
  total: number
  page: number
  page_size: number
}

export async function listSharedUsers(params: Record<string, unknown> = {}) {
  const { data } = await apiClient.get<AdminSharedUsersPage>('/admin/shared-pool/users', { params })
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

export interface AdminSharedRevenueSummary {
  user_id: number
  username?: string
  email?: string
  listing_count: number
  request_count: number
  gross_amount: string
  platform_fee: string
  owner_amount: string
  pending: string
  available: string
  frozen: string
  total_earned: string
  total_transferred: string
  last_request_at?: string
}

export interface AdminSharedRevenueRecord {
  id: number
  listing_id: number
  listing_name: string
  platform: string
  owner_user_id: number
  owner_name?: string
  owner_email?: string
  consumer_user_id: number
  consumer_name?: string
  consumer_email?: string
  request_id: string
  model?: string
  result_status?: string
  duration_ms?: number
  gross_cost: string
  platform_fee: string
  owner_amount: string
  frozen_until?: string
  released_at?: string
  created_at: string
}

export interface AdminSharedRevenuePage {
  items: AdminSharedRevenueSummary[]
  total: number
  page: number
  page_size: number
}

export interface AdminSharedRevenueRecordsPage {
  items: AdminSharedRevenueRecord[]
  total: number
  page: number
  page_size: number
}

export async function listSharedRevenue(params: Record<string, unknown> = {}) {
  const { data } = await apiClient.get<AdminSharedRevenuePage>('/admin/shared-pool/revenue', { params })
  return data
}

export async function listSharedRevenueRecords(params: Record<string, unknown> = {}) {
  const { data } = await apiClient.get<AdminSharedRevenueRecordsPage>('/admin/shared-pool/revenue/records', { params })
  return data
}
