import apiClient from '../client'

export interface PassiveMetric { enabled: boolean; pause: number; recover: number }
export interface PassiveCacheWindow {
  window_seconds: number; max_samples: number; min_samples: number; confirmations: number; new_samples: number
  min_tokens: number; min_sessions: number; max_session_share_percent: number
}
export interface PassiveCacheProtection {
  action: 'observe' | 'reduce'; quick: PassiveCacheWindow; action_window: PassiveCacheWindow
  allocation_percent: number; recovery_step_percent: number; recovery_confirmations: number
  min_hold_seconds: number; recovery_hold_seconds: number; max_reduced_percent: number; min_serving_accounts: number
  reuse_window_seconds: number; warmup_requests: number
}
export interface PassiveMetricState {
  recovery_hits?: number; evidence?: PassiveEvidence
  phase: string; reason: string; hits: number; last_evidence_at: string; observe_after: string; hold_until: string; updated_at: string
}
export interface PassiveCacheWindowEvidence {
  input_cost?: number | null; input_cost_samples?: number; account_switches?: number; ttft_p90?: number | null; ttft_samples?: number
  total_samples: number; reported_samples: number; reusable_samples: number; cold_samples: number; unknown_samples: number
  missing_telemetry_samples: number; failed_samples?: number; cache_rate: number | null; protection_rate: number | null
  read_tokens: number; input_tokens: number; eligible_read_tokens: number; eligible_input_tokens: number; sessions: number
  max_session_share_percent: number; fresh: number; latest: string; complete: boolean; window_seconds: number; max_samples: number
  reasons?: Record<string, number>; breached_sessions?: number; healthy_sessions?: number
}
export interface PassiveCacheEvidence { model: string; quick: PassiveCacheWindowEvidence; action: PassiveCacheWindowEvidence }
export interface PassiveCacheState extends Omit<PassiveMetricState, 'evidence'> {
  quick_hits: number; recovery_hits: number; allocation_percent: number; quick_evidence_at: string; evidence: PassiveCacheEvidence
}
export interface PassivePolicy {
  schema_version?: number; ttft_action?: 'observe' | 'limit'; ttft_concurrency_limit?: number; ttft_priority_penalty?: number
  cache_protection?: PassiveCacheProtection
  id: number; version: number; updated_at?: string
  name: string; enabled: boolean; mode: 'observe' | 'enforce'; group_ids: number[]; excluded_account_ids: number[]
  action: 'pause' | 'limit'; concurrency_limit: number; priority_penalty: number
  model: string; scope: 'model' | 'account'; window_seconds: number; max_samples: number; min_samples: number
  confirmations: number; new_samples: number; error_rate: PassiveMetric; ttft: PassiveMetric; cache_rate: PassiveMetric
  consecutive_failures: number; cooldown_seconds: number; max_cooldown_seconds: number; trial_seconds: number
  trial_requests: number; trial_concurrency: number; recovery_samples: number
}
export interface PassiveEvidence {
  cache_read_tokens?: number; cache_input_tokens?: number
  samples: number; failures: number; consecutive_failures: number; error_rate: number | null; ttft: number | null
  ttft_samples: number; cache_rate: number | null; cache_samples: number; fresh: number; complete: boolean; latest: string
}
export interface PassiveState {
  latency?: PassiveMetricState; cache?: Record<string, PassiveCacheState>; allocation_percent?: number
  policy_id: number; policy_name: string; account_id: number; account_name?: string; model: string; probe_model?: string; scope: string; mode: string
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
  act: async (s: PassiveState, action: string, seconds = 0) => { await apiClient.post(`/admin/passive-quality-policies/${s.policy_id}/accounts/${s.account_id}/action`, { action, seconds, ...(s.model ? { model: s.model } : {}) }) },
}
export function defaultPassivePolicy(): PassivePolicy {
  return { schema_version: 2, ttft_action: 'limit', ttft_concurrency_limit: 2, ttft_priority_penalty: 50, cache_protection: defaultPassiveCacheProtection(), id: 0, version: 0, name: '', enabled: true, mode: 'observe', group_ids: [], excluded_account_ids: [], model: '', scope: 'model', action: 'pause', concurrency_limit: 2, priority_penalty: 10,
    window_seconds: 600, max_samples: 100, min_samples: 30, confirmations: 2, new_samples: 5,
    error_rate: { enabled: true, pause: 20, recover: 5 }, ttft: { enabled: false, pause: 10000, recover: 5000 },
    cache_rate: { enabled: false, pause: 10, recover: 20 }, consecutive_failures: 0,
    cooldown_seconds: 120, max_cooldown_seconds: 3600, trial_seconds: 300, trial_requests: 50, trial_concurrency: 1, recovery_samples: 20 }
}

export function defaultPassiveCacheProtection(): PassiveCacheProtection {
  return {
    action: 'reduce',
    quick: { window_seconds: 300, max_samples: 50, min_samples: 15, confirmations: 2, new_samples: 3, min_tokens: 32768, min_sessions: 3, max_session_share_percent: 60 },
    action_window: { window_seconds: 900, max_samples: 200, min_samples: 30, confirmations: 3, new_samples: 5, min_tokens: 65536, min_sessions: 3, max_session_share_percent: 60 },
    allocation_percent: 25, recovery_step_percent: 25, recovery_confirmations: 2, min_hold_seconds: 300, recovery_hold_seconds: 300,
    max_reduced_percent: 50, min_serving_accounts: 1, reuse_window_seconds: 300, warmup_requests: 1,
  }
}
// Editing a legacy policy never implicitly changes its evaluation semantics.
export function passivePolicyDraft(policy?: PassivePolicy): PassivePolicy {
  if (!policy) return defaultPassivePolicy()
  const draft = { ...defaultPassivePolicy(), ...JSON.parse(JSON.stringify(policy)) } as PassivePolicy
  if (policy.schema_version !== 2) {
    draft.schema_version = policy.schema_version || 0
    if (!policy.model || !policy.scope) draft.scope = 'account'
    delete draft.ttft_action; delete draft.ttft_concurrency_limit; delete draft.ttft_priority_penalty; delete draft.cache_protection
  }
  return draft
}
export function upgradePassivePolicy(policy: PassivePolicy): PassivePolicy {
  return { ...passivePolicyDraft(policy), schema_version: 2, mode: 'observe', scope: 'model', ttft_action: 'limit', ttft_concurrency_limit: 2, ttft_priority_penalty: 50, cache_protection: defaultPassiveCacheProtection() }
}
export function passiveCacheValidation(policy: PassivePolicy): string {
  if (policy.schema_version !== 2) return ''
  if (!['observe', 'limit'].includes(policy.ttft_action || '') || !Number.isInteger(policy.ttft_concurrency_limit) || policy.ttft_concurrency_limit! < 1 || policy.ttft_concurrency_limit! > 1000 || !Number.isInteger(policy.ttft_priority_penalty) || policy.ttft_priority_penalty! < 0 || policy.ttft_priority_penalty! > 1000) return 'invalidMetricActions'
  const c = policy.cache_protection
  if (!c || !['observe', 'reduce'].includes(c.action)) return 'invalidCacheConfig'
  for (const w of [c.quick, c.action_window]) {
    if (!w || !Number.isInteger(w.window_seconds) || w.window_seconds < 60 || w.window_seconds > 86400 || !Number.isInteger(w.max_samples) || w.max_samples < 5 || w.max_samples > 2000 || !Number.isInteger(w.min_samples) || w.min_samples < 5 || w.min_samples > w.max_samples || !Number.isInteger(w.confirmations) || w.confirmations < 1 || w.confirmations > 10 || !Number.isInteger(w.new_samples) || w.new_samples < 1 || w.new_samples > w.max_samples || !Number.isInteger(w.min_tokens) || w.min_tokens < 1 || w.min_tokens > 1000000000 || !Number.isInteger(w.min_sessions) || w.min_sessions < 2 || w.min_sessions > Math.floor(w.min_samples / 2) || !Number.isInteger(w.max_session_share_percent) || w.max_session_share_percent < 10 || w.max_session_share_percent > 90 || w.max_session_share_percent * w.min_sessions < 100) return 'invalidCacheWindow'
  }
  if ((['window_seconds', 'max_samples', 'min_samples', 'confirmations', 'new_samples', 'min_tokens', 'min_sessions'] as const).some(key => c.action_window[key] < c.quick[key])) return 'invalidCacheOrder'
  const bounds: [keyof PassiveCacheProtection, number, number][] = [['allocation_percent', 1, 99], ['recovery_step_percent', 1, 99], ['recovery_confirmations', 1, 10], ['min_hold_seconds', 30, 86400], ['recovery_hold_seconds', 30, 86400], ['max_reduced_percent', 1, 90], ['min_serving_accounts', 1, 10000], ['reuse_window_seconds', 30, 3600], ['warmup_requests', 1, 20]]
  return bounds.some(([key, min, max]) => !Number.isInteger(c[key]) || Number(c[key]) < min || Number(c[key]) > max) ? 'invalidCacheConfig' : ''
}
