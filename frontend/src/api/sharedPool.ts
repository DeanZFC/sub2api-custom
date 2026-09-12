import { apiClient } from './client'

export interface SharedCall {
  request_id: string
  model?: string
  result_status: string
  duration_ms?: number
  charged_amount: number
  created_at: string
}
export interface SharedCard {
  id: number
  /** Underlying accounts.id used by the account test endpoint. */
  account_id?: number
  uploader_name?: string
  platform: string
  /** Underlying account type: oauth, apikey, setup-token, bedrock, service_account. */
  type?: string
  display_name: string
  status: string
  concurrency_limit: number
  concurrency_multiplier: number
  sell_rate: number
  listed?: boolean
  current_concurrency?: number
  total_call_count: number
  last_called_at?: string
  available_models?: string[]
  recent_calls: SharedCall[]
}
export interface SharedWallet {
  pending: string
  available: string
  frozen: string
  total_earned: string
  total_transferred: string
}
export interface SharedTransfer {
  id: number
  amount: string
  balance_after: string
}
export async function getSharedPoolCards(
  params: { platform?: string; limit?: number; recent_limit?: number } = {}
) {
  const { data } = await apiClient.get<{ items: SharedCard[] }>('/user/shared-pool/cards', { params })
  return data.items
}
export async function getMySharedCards() {
  const { data } = await apiClient.get<{ items: SharedCard[] }>('/user/shared-pool/my-cards')
  return data.items
}
export async function getSharedWallet() {
  const { data } = await apiClient.get<SharedWallet>('/user/shared-pool/wallet')
  return data
}
export async function transferSharedEarnings(key: string) {
  const { data } = await apiClient.post<SharedTransfer>('/user/shared-pool/wallet/transfer', null, {
    headers: { 'Idempotency-Key': key }
  })
  return data
}

export interface SharedUploadInput {
  name: string; platform: string; type: string; credentials: Record<string, unknown>
  concurrency?: number; concurrency_multiplier?: number; sell_rate?: number
  proxy_url?: string
  extra?: Record<string, unknown>
  expires_at?: string
}
export async function createSharedListing(input: SharedUploadInput) {
  const { data } = await apiClient.post<{ id: number; status: string; account_id: number }>('/user/shared-pool/listings', input)
  return data
}
export async function setSharedListingStatus(id: number, action: 'pause' | 'resume') {
  const { data } = await apiClient.post<{ id: number; status: string }>(`/user/shared-pool/listings/${id}/${action}`)
  return data
}
export async function setSharedListingListed(id: number, listed: boolean) {
  const { data } = await apiClient.put<{ id: number; listed: boolean }>(`/user/shared-pool/listings/${id}/listed`, { listed })
  return data
}
export async function deleteSharedListing(id: number) { await apiClient.delete(`/user/shared-pool/listings/${id}`) }

export async function getSharedAccountModels(accountId: number) {
  const { data } = await apiClient.get<Array<{ id: string; display_name?: string; type?: string }>>(`/user/shared-pool/accounts/${accountId}/models`)
  return data
}

export async function getSharedAccount(accountId: number) {
  const { data } = await apiClient.get<import('@/types').Account>(`/user/shared-pool/accounts/${accountId}`)
  return data
}
export async function updateSharedAccount(accountId: number, input: Record<string, unknown>) {
  const { data } = await apiClient.put<import('@/types').Account>(`/user/shared-pool/accounts/${accountId}`, input)
  return data
}
export async function applySharedOAuthCredentials(
  accountId: number,
  input: { type: 'oauth' | 'setup-token'; credentials: Record<string, unknown>; extra?: Record<string, unknown> }
) {
  const { data } = await apiClient.post<import('@/types').Account>(`/user/shared-pool/accounts/${accountId}/apply-oauth-credentials`, input)
  return data
}
export async function clearSharedAccountError(accountId: number) {
  const { data } = await apiClient.post<import('@/types').Account>(`/user/shared-pool/accounts/${accountId}/clear-error`)
  return data
}

export interface SharedUpstreamModelCatalog {
  models: string[]
  metadata?: Record<string, unknown>
  warnings?: Array<{ code: string; message: string }>
}
export async function syncSharedUpstreamModels(accountId: number) {
  const { data } = await apiClient.post<SharedUpstreamModelCatalog>(`/user/shared-pool/accounts/${accountId}/models/sync-upstream`)
  return data
}
export async function syncSharedUpstreamModelsPreview(params: {
  platform: string
  type: string
  base_url?: string
  api_key: string
  model_mapping?: Record<string, string>
}) {
  const { data } = await apiClient.post<SharedUpstreamModelCatalog>('/user/shared-pool/models/sync-upstream-preview', params)
  return data
}

export type SharedKeySelectionMode = 'manual' | 'platform'
export type SharedKeyPriorityMode = 'order' | 'rate' | 'availability'
export interface SharedAPIKey {
  id: number
  name: string
  key?: string
  key_preview: string
  platform: string
  status: string
  selection_mode?: SharedKeySelectionMode
  priority_mode?: SharedKeyPriorityMode
  listing_ids: number[]
  created_at: string
}
export interface SharedAPIKeyInput {
  name: string
  platform: string
  selection_mode?: SharedKeySelectionMode
  priority_mode?: SharedKeyPriorityMode
  listing_ids: number[]
  status?: string
}
export async function listSharedAPIKeys(){ const {data}=await apiClient.get<{items:SharedAPIKey[]}>('/user/shared-pool/api-keys'); return data.items }
export async function createSharedAPIKey(input: SharedAPIKeyInput){ const {data}=await apiClient.post<SharedAPIKey & {key:string}>('/user/shared-pool/api-keys',input); return data }
export async function updateSharedAPIKey(id:number,input: SharedAPIKeyInput){ await apiClient.put(`/user/shared-pool/api-keys/${id}`,input) }
export async function deleteSharedAPIKey(id:number){ await apiClient.delete(`/user/shared-pool/api-keys/${id}`) }
