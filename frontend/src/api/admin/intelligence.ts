import { apiClient } from '../client'

export interface IntelligenceObservation {
  result_id: number
  account_id: number
  account_name: string
  model: string
  verdict: 'pass' | 'suspect' | 'confirmed' | 'unknown'
  artwork: 'default' | 'missing' | 'skipped'
  reviewed: '' | 'pass' | 'fail'
  review_reason?: string
  evidence?: {
    auto_verdict: IntelligenceObservation['verdict']
    checks: {
      key: 'fingerprint' | 'codex_state' | 'candy' | 'http_429'
      status: 'pass' | 'fail' | 'unknown'
      detail: string
    }[]
  }
  retryable_artwork?: boolean
  finished_at: string
}
export interface IntelligenceAccountState {
  account_id: number
  account_name: string
  model: string
  blocked: boolean
  passes: number
  verdict: IntelligenceObservation['verdict']
  next_check?: string
}
export interface IntelligenceOverview {
  results: IntelligenceObservation[]
  states: IntelligenceAccountState[]
}

export async function policyOverview(id: number, signal?: AbortSignal) {
  return (await apiClient.get<IntelligenceOverview>(`/admin/test-plans/${id}/intelligence`, { signal })).data
}
export async function policyArtwork(id: number, signal?: AbortSignal) {
  return (await apiClient.get<{ html: string }>(`/admin/test-results/${id}/intelligence-artwork`, { signal })).data.html
}
export async function reviewPolicyArtwork(id: number, verdict: 'pass' | 'fail', reason: string) {
  await apiClient.post(`/admin/test-results/${id}/intelligence-review`, { verdict, reason })
}

export interface IntelligenceScope {
  targets: number
  account_ids: number[]
  group_counts: Record<string, number>
}
export async function intelligenceScope(
  group_ids: number[],
  model_ids: string[],
  excluded_account_ids: number[],
  signal?: AbortSignal,
) {
  return (
    await apiClient.post<IntelligenceScope>(
      '/admin/intelligence/scope',
      { group_ids, model_ids, excluded_account_ids },
      { signal },
    )
  ).data
}

export async function releaseIntelligenceHold(planId: number, accountId: number, model: string) {
  await apiClient.post(`/admin/test-plans/${planId}/intelligence-release`, { account_id: accountId, model })
}
