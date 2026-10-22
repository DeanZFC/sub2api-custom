import apiClient from '../client'

export interface PassiveMetric { enabled: boolean; pause: number; recover: number }
export interface PassivePolicy {
  id: number; version: number; updated_at?: string
  name: string; enabled: boolean; mode: 'observe' | 'enforce'; group_ids: number[]; excluded_account_ids: number[]
  action: 'pause' | 'limit'; concurrency_limit: number; priority_penalty: number
  model: string; scope: 'model' | 'account'; window_seconds: number; max_samples: number; min_samples: number
  confirmations: number; new_samples: number; error_rate: PassiveMetric; ttft: PassiveMetric; cache_rate: PassiveMetric
  consecutive_failures: number; cooldown_seconds: number; max_cooldown_seconds: number; trial_seconds: number
  trial_requests: number; trial_concurrency: number; recovery_samples: number
}
export interface PassiveEvidence {
  samples: number; failures: number; consecutive_failures: number; error_rate: number | null; ttft: number | null
  ttft_samples: number; cache_rate: number | null; cache_samples: number; fresh: number; complete: boolean; latest: string
}
export interface PassiveState {
  policy_id: number; policy_name: string; account_id: number; account_name?: string; model: string; scope: string; mode: string
  phase: string; reason: string; hits: number; backoff: number; used: number; inflight: number; generation: number
  next_at: string; trial_started_at: string; trial_ends_at: string; updated_at: string; evidence: PassiveEvidence; other_hold: boolean
}
export interface PassiveEvent { id: number; policy_id: number; account_id: number; policy_name: string; kind: string; detail: PassiveState; created_at: string }
export const passiveQualityAPI = {
  policies: async () => (await apiClient.get<PassivePolicy[]>('/admin/passive-quality-policies')).data,
  save: async (policy: PassivePolicy) => (await (policy.id
    ? apiClient.put<PassivePolicy>(`/admin/passive-quality-policies/${policy.id}`, policy)
    : apiClient.post<PassivePolicy>('/admin/passive-quality-policies', policy))).data,
  remove: async (id: number) => { await apiClient.delete(`/admin/passive-quality-policies/${id}`) },
  states: async () => (await apiClient.get<PassiveState[]>('/admin/passive-quality-states')).data,
  events: async (before?: number, accountID?: number) => (await apiClient.get<PassiveEvent[]>('/admin/passive-quality-events', { params: { before, account_id: accountID || undefined } })).data,
  act: async (s: PassiveState, action: string, seconds = 0) => { await apiClient.post(`/admin/passive-quality-policies/${s.policy_id}/accounts/${s.account_id}/action`, { action, seconds }) },
}
export function defaultPassivePolicy(): PassivePolicy {
  return { id: 0, version: 0, name: '', enabled: true, mode: 'observe', group_ids: [], excluded_account_ids: [], model: '', scope: 'model', action: 'pause', concurrency_limit: 2, priority_penalty: 10,
    window_seconds: 600, max_samples: 100, min_samples: 30, confirmations: 2, new_samples: 5,
    error_rate: { enabled: true, pause: 20, recover: 5 }, ttft: { enabled: false, pause: 10000, recover: 5000 },
    cache_rate: { enabled: false, pause: 10, recover: 20 }, consecutive_failures: 0,
    cooldown_seconds: 120, max_cooldown_seconds: 3600, trial_seconds: 300, trial_requests: 50, trial_concurrency: 1, recovery_samples: 20 }
}
