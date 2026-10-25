import { apiClient } from '../client'

const requestOptions = { timeout: 120_000 }

export interface OpenAIVMConfig {
  enabled: boolean
  base_url: string
  proxy_url: string
  secret_configured: boolean
}

export interface OpenAIVMConfigInput {
  enabled: boolean
  base_url: string
  proxy_url: string
  // Omitted or empty means keep the configured secret. Never read it back.
  secret?: string
}

export type OpenAIVMRuntime = 'docker' | 'kvm'

export interface OpenAIVMBinding {
  account_id: number
  slot_id: string
  status: string
  last_error: string | null
  created_at: string
  updated_at: string
}

export interface OpenAIVMAccountState {
  binding: OpenAIVMBinding | null
  eligible: boolean
  configured: boolean
}

export interface OpenAIVMHealth {
  healthy: boolean
  message?: string
}

export async function getConfig(): Promise<OpenAIVMConfig> {
  const { data } = await apiClient.get<OpenAIVMConfig>('/admin/openai-vm/config', requestOptions)
  return data
}

export async function saveConfig(config: OpenAIVMConfigInput): Promise<OpenAIVMConfig> {
  const { data } = await apiClient.put<OpenAIVMConfig>('/admin/openai-vm/config', config, requestOptions)
  return data
}

// Tests the saved bridge configuration only; never provisions a slot.
export async function testConnection(): Promise<OpenAIVMHealth> {
  const { data } = await apiClient.post<OpenAIVMHealth>('/admin/openai-vm/test', undefined, requestOptions)
  return data
}

export async function getBinding(accountId: number): Promise<OpenAIVMAccountState> {
  const { data } = await apiClient.get<OpenAIVMAccountState>(`/admin/accounts/${accountId}/vm`, requestOptions)
  return data
}

export async function createBinding(accountId: number, runtimeType: OpenAIVMRuntime): Promise<OpenAIVMBinding> {
  const { data } = await apiClient.post<OpenAIVMBinding>(`/admin/accounts/${accountId}/vm`, { runtime_type: runtimeType }, requestOptions)
  return data
}

export async function retryBinding(accountId: number): Promise<OpenAIVMBinding | null> {
  const { data } = await apiClient.post<OpenAIVMBinding | null>(`/admin/accounts/${accountId}/vm/retry`, undefined, requestOptions)
  return data
}

export async function deleteBinding(accountId: number): Promise<void> {
  await apiClient.delete(`/admin/accounts/${accountId}/vm`, requestOptions)
}

export const openaiVmAPI = { getConfig, saveConfig, testConnection, getBinding, createBinding, retryBinding, deleteBinding }
