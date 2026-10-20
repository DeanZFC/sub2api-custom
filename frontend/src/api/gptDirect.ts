import { apiClient } from './client'

export interface GPTDirectSettings {
  enabled: boolean
  base_url: string
  tenant_slug: string
  purchase_url: string
  button_text: string
  description: string
  support_url: string // Legacy key: contact text or an HTTP(S) link.
  lottery_reward_enabled: boolean
  lottery_chances: number
}

export type GPTDirectConfig = Omit<GPTDirectSettings, 'base_url' | 'tenant_slug'> & { receipt_scope: string }
export interface GPTDirectCode {
  valid: boolean
  product: string
  plan: string
  planName: string
  planKind: string
  status: string
  email: string
  taskStatus?: string
}
export interface GPTDirectTask {
  id: string
  status: 'manual_pending' | 'queued' | 'running' | 'success' | 'failed' | 'cancelled'
  progress: number
  email: string
  plan: string
  planName: string
  planKind: string
  fulfillmentKind: string
  product: string
  createdAt: string
  lottery_reward?: { chances: number; status: 'pending' | 'awarded' | 'not_eligible' }
}
export interface GPTDirectReceipt { task: GPTDirectTask; token: string }
export interface GPTDirectSubscription {
  email: string
  plan: string
  planKey: string
  active: boolean
  hasActiveSubscription: boolean
  expiresAt: string
  remainingDays: number | null
}

export const gptDirectAPI = {
  async config() { return (await apiClient.get<GPTDirectConfig>('/gpt-direct/config')).data },
  async settings() { return (await apiClient.get<GPTDirectSettings>('/admin/gpt-direct/settings')).data },
  async saveSettings(value: GPTDirectSettings) {
    return (await apiClient.put<GPTDirectSettings>('/admin/gpt-direct/settings', value)).data
  },
  async verify(code: string, key?: string) {
    return (await apiClient.post<GPTDirectCode>('/gpt-direct/verify', { code }, key ? { headers: { 'Idempotency-Key': key } } : undefined)).data
  },
  async query(code: string) { return (await apiClient.post<GPTDirectCode>('/gpt-direct/query', { code })).data },
  async subscription(session: string) {
    return (await apiClient.post<GPTDirectSubscription>('/gpt-direct/subscription', { session }, { timeout: 60000 })).data
  },
  async redeem(code: string, session: string, key: string) {
    return (await apiClient.post<GPTDirectReceipt>('/gpt-direct/redeem', { code, session }, {
      headers: { 'Idempotency-Key': key }, timeout: 60000,
    })).data
  },
  async task(id: string, token: string) {
    // Capabilities stay out of browser URLs, access logs and history.
    return (await apiClient.post<GPTDirectTask>('/gpt-direct/task', { id, token }, { timeout: 60000 })).data
  },
}
