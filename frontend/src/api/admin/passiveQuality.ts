import apiClient from '../client'

export interface PassiveMetric { enabled: boolean; pause: number; recover: number }
export interface GroupQualityStability {
  availability_pause_seconds: number; performance_pause_seconds: number; recovery_seconds: number
}
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
  purpose?: 'cache_target' | 'group_quality_target'
  recovery_mode?: 'active'
  stability?: GroupQualityStability
  schema_version?: number; ttft_action?: 'observe' | 'limit'; ttft_concurrency_limit?: number; ttft_priority_penalty?: number
  tps_action?: 'observe' | 'limit'; tps_concurrency_limit?: number; tps_priority_penalty?: number
  cache_protection?: PassiveCacheProtection
  id: number; version: number; updated_at?: string
  name: string; enabled: boolean; mode: 'observe' | 'enforce'; group_ids: number[]; excluded_account_ids: number[]
  action: 'pause' | 'limit'; concurrency_limit: number; priority_penalty: number
  model: string; scope: 'model' | 'account'; window_seconds: number; max_samples: number; min_samples: number
  confirmations: number; new_samples: number; error_rate: PassiveMetric; ttft: PassiveMetric; tps: PassiveMetric; cache_rate: PassiveMetric
  consecutive_failures: number; cooldown_seconds: number; max_cooldown_seconds: number; trial_seconds: number
  trial_requests: number; trial_concurrency: number; recovery_samples: number
}
export interface PassiveEvidence {
  output_tps?: number | null; tps_samples?: number; tps_fresh?: number; tps_latest?: string; fresh_output_tps?: number | null
  cache_fresh?: number; cache_latest?: string; ttft_fresh?: number; ttft_latest?: string
  fresh_error_rate?: number | null; fresh_ttft?: number | null; fresh_cache_rate?: number | null
  cache_read_tokens?: number; cache_input_tokens?: number
  samples: number; failures: number; consecutive_failures: number; error_rate: number | null; ttft: number | null
  ttft_samples: number; cache_rate: number | null; cache_samples: number; fresh: number; complete: boolean; latest: string
}
export interface PassiveActiveRecoveryResult {
  evidence: PassiveEvidence
  started_at: string; finished_at: string; deferred_until?: string
  reason: string; verdict: 'pass' | 'fail' | 'pending' | 'deferred'; passed: boolean
  requests: number; warmup_requests: number; batches: number
  evidence_batches?: PassiveEvidence[]
}
export interface PassiveState {
  purpose?: 'cache_target' | 'group_quality_target'
  active_recovery?: PassiveActiveRecoveryResult
  recovered_at?: string
  suspicion_since?: string; recovery_since?: string; recovery_hits?: number; cache_allocation_reduced?: boolean; stability_evidence_at?: string; confirmation_metric?: string
  speed?: PassiveMetricState; latency?: PassiveMetricState; cache?: Record<string, PassiveCacheState>; allocation_percent?: number
  policy_id: number; policy_name: string; account_id: number; account_name?: string; model: string; probe_model?: string; scope: string; mode: string
  phase: string; reason: string; hits: number; backoff: number; used: number; inflight: number; generation: number
  next_at: string; trial_started_at: string; trial_ends_at: string; updated_at: string; evidence: PassiveEvidence; other_hold: boolean
}
export interface PassiveEvent { id: number; policy_id: number; account_id: number; policy_name: string; kind: string; detail: PassiveState; created_at: string }
export interface GroupQualityTargetDraft {
  id: number; version: number; purpose: 'group_quality_target'; name: string; enabled: boolean; mode: 'observe' | 'enforce'; model: string
  recovery_mode: 'active'
  stability: GroupQualityStability
  group_ids: number[]; cache_rate: PassiveMetric; error_rate: PassiveMetric; ttft: PassiveMetric; tps: PassiveMetric
  excluded_account_ids: number[]
  window_seconds?: number; max_samples?: number; min_samples?: number; confirmations?: number; new_samples?: number
  cooldown_seconds?: number; max_cooldown_seconds?: number
}
export interface GroupQualityTargetSummary {
  snapshot_ready?: boolean; stale?: boolean; window_started_at?: string
  contributors?: GroupQualityContributor[]; contributor_total?: number; contributors_truncated?: boolean
  policy_id: number; group_id: number; window_seconds: number; actual_cache_rate: number | null
  cache_samples: number; cache_read_tokens: number; cache_input_tokens: number; cache_unknown_samples: number
  actual_availability: number | null; success_samples: number; failure_samples: number; failure_unknown_samples: number
  output_tps?: number | null; tps_samples?: number
  ttft_p90_ms: number | null; ttft_samples: number; generated_at: string
  telemetry_complete: boolean
}
export interface GroupQualityContributor {
  output_tps?: number | null; tps_samples?: number
  account_id: number; account_name: string; model: string; traffic_kind: 'normal' | 'validation' | 'fallback' | 'unattributed'
  success_samples: number; failure_samples: number; cache_samples: number; cache_unknown_samples: number
  cache_input_tokens: number; cache_read_tokens: number; uncached_tokens: number; ttft_samples: number; slow_requests: number; ttft_p90_ms: number | null
}
export interface PassiveQuery { policy_id?: number; account_id?: number; model?: string; phase?: string; kind?: string; since?: string; until?: string; cursor?: string; limit?: number }
export interface PassivePage<T> { items: T[]; next_cursor?: string; has_more: boolean; total?: number }
export interface PassiveStateSummary { total: number; paused_accounts: number; limited_accounts: number; trial_accounts: number; observed_accounts: number; by_policy: { policy_id: number; total: number; phases: Record<string, number>; verified: number }[] }
export interface PassiveRuntime { queue_depth: number; queue_capacity: number; pending_records: number; dropped_records: number; pending_gap_scopes: number; last_persisted_at: string | null; last_persist_error: string; last_sweep_started_at: string | null; last_sweep_succeeded_at: string | null; last_sweep_duration_ms: number; last_sweep_error: string; sweep_skipped_backlog: number; last_cleanup_succeeded_at: string | null; last_cleanup_error: string; collection_lag_seconds: number }
export const emptyPassiveSummary = (): PassiveStateSummary => ({ total: 0, paused_accounts: 0, limited_accounts: 0, trial_accounts: 0, observed_accounts: 0, by_policy: [] })
export const passiveQualityAPI = {
  policies: async () => (await apiClient.get<PassivePolicy[]>('/admin/passive-quality-policies')).data,
  remove: async (id: number) => { await apiClient.delete(`/admin/passive-quality-policies/${id}`) },
  states: async () => (await apiClient.get<PassiveState[]>('/admin/passive-quality-states')).data,
  statePage: async (params: PassiveQuery = {}) => (await apiClient.get<PassivePage<PassiveState>>('/admin/passive-quality-states', { params: { ...params, paginated: true } })).data,
  stateSummary: async () => (await apiClient.get<PassiveStateSummary>('/admin/passive-quality-states/summary')).data,
  eventPage: async (params: PassiveQuery = {}) => (await apiClient.get<PassivePage<PassiveEvent>>('/admin/passive-quality-events', { params: { ...params, paginated: true } })).data,
  runtime: async () => (await apiClient.get<PassiveRuntime>('/admin/passive-quality/runtime')).data,
  groupTargets: async () => (await apiClient.get<GroupQualityTargetSummary[]>('/admin/passive-quality/group-targets')).data,
  saveGroupQualityTarget: async (policy: GroupQualityTargetDraft) => (await (policy.id
    ? apiClient.put<PassivePolicy>(`/admin/passive-quality-policies/${policy.id}`, policy)
    : apiClient.post<PassivePolicy>('/admin/passive-quality-policies', policy))).data,
  events: async (before?: number, accountID?: number) => (await apiClient.get<PassiveEvent[]>('/admin/passive-quality-events', { params: { before, account_id: accountID || undefined } })).data,
  act: async (s: PassiveState, action: string, seconds = 0) => { await apiClient.post(`/admin/passive-quality-policies/${s.policy_id}/accounts/${s.account_id}/action`, { action, seconds, ...(s.model ? { model: s.model } : {}) }) },
}

function passiveTPSDraft(metric?: PassiveMetric): PassiveMetric {
  return metric && (metric.enabled || metric.pause !== 0 || metric.recover !== 0)
    ? { ...metric } : { enabled: false, pause: 10, recover: 15 }
}

// The server supplies and validates the sampling and recovery defaults.
export function isGroupQualityTarget(policy: Pick<PassivePolicy, 'purpose'>): boolean {
  return policy.purpose === 'group_quality_target' || policy.purpose === 'cache_target'
}
export function groupQualityTargetDraft(policy?: PassivePolicy, _groupName = ''): GroupQualityTargetDraft {
  const percentage = 80
  const ttft = 15000
  const draft: GroupQualityTargetDraft = {
    id: policy?.id || 0, version: policy?.version || 0, purpose: 'group_quality_target', name: policy?.name || '',
    recovery_mode: 'active', window_seconds: 3600, max_samples: 0,
    enabled: policy?.enabled ?? true, mode: policy?.mode || 'enforce', model: policy?.model || '', group_ids: policy ? [...policy.group_ids] : [],
    stability: { availability_pause_seconds: policy?.stability?.availability_pause_seconds ?? 60, performance_pause_seconds: policy?.stability?.performance_pause_seconds ?? 300, recovery_seconds: policy?.stability?.recovery_seconds ?? 120 },
    excluded_account_ids: [...(policy?.excluded_account_ids || [])],
    cache_rate: { enabled: policy?.cache_rate.enabled ?? true, pause: policy?.cache_rate.pause ?? percentage, recover: policy?.cache_rate.recover ?? percentage + 5 },
    error_rate: { enabled: policy?.error_rate.enabled ?? true, pause: policy?.error_rate.pause ?? 10, recover: policy?.error_rate.recover ?? 5 },
    tps: policy ? passiveTPSDraft(policy.tps) : { enabled: true, pause: 30, recover: 36 },
    ttft: { enabled: policy?.ttft.enabled ?? true, pause: policy?.ttft.pause ?? ttft, recover: policy?.ttft.recover ?? ttft * .8 },
  }
  if (policy) {
    for (const key of ['cooldown_seconds', 'max_cooldown_seconds'] as const) {
      if (policy[key] !== undefined) draft[key] = policy[key]
    }
  }
  draft.min_samples = Math.max(20, policy?.min_samples || 20)
  draft.new_samples = Math.max(5, policy?.new_samples || 5)
  draft.confirmations = Math.max(2, policy?.confirmations || 2)
  return draft
}
