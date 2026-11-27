import { apiClient } from './client'

export type IntelligenceVerdict = 'pass' | 'suspect' | 'degraded' | 'unknown'
export type IntelligenceCounts = Record<IntelligenceVerdict, number>
export interface IntelligenceModelCounts {
  model: string
  pass: number
  degraded: number
}
export type ArtworkStatus = 'default' | 'approved' | 'veto' | 'missing' | 'skipped'
export interface IntelligenceGroup {
  id: number
  name: string
  models: string[]
  has_legacy_results?: boolean
}
export interface IntelligenceRound {
  id: string
  started_at: string
  finished_at: string
  verdict: IntelligenceVerdict
  counts: IntelligenceCounts
  model_counts: IntelligenceModelCounts[]
  accounts: number
  targets: number
  passed_targets: number
}
export interface IntelligenceModel {
  model: string
  effort: string
  finished_at: string | null
  verdict: IntelligenceVerdict
  checks: { key: 'fingerprint' | 'codex_state' | 'candy' | 'rate_limit'; status: 'pass' | 'fail' | 'unknown'; detail: string }[]
  artwork_status: ArtworkStatus
  artwork_id?: number
}
export interface RoundAccount {
  alias: string
  verdict: IntelligenceVerdict
  targets: number
  passed_targets: number
  models: IntelligenceModel[]
}
interface Page {
  page: number
  page_size: number
  total: number
}
export interface RoundDetail extends Page {
  round: IntelligenceRound
  accounts: RoundAccount[]
}
export interface IntelligenceSummary extends Page {
  rounds: number
  accounts: number
  participations: number
  counts: IntelligenceCounts
  targets: number
  passed_targets: number
  items: {
    alias: string
    participations: number
    counts: IntelligenceCounts
    models: { model: string; effort: string; counts: IntelligenceCounts; last_tested_at: string | null }[]
  }[]
}
const base = '/user/intelligence/groups'
export async function intelligenceDirectory(signal?: AbortSignal) {
  return (await apiClient.get<{ rounds_visible: boolean; groups: IntelligenceGroup[] }>('/user/intelligence/directory', { signal })).data
}
export async function intelligenceRounds(group: number, model?: string, signal?: AbortSignal) {
  return (
    (await apiClient.get<{ rounds: IntelligenceRound[] }>(`${base}/${group}/rounds`, { params: { model }, signal })).data
      .rounds || []
  )
}
export async function intelligenceRound(group: number, run: string, model?: string, page = 1, signal?: AbortSignal) {
  return (
    await apiClient.get<RoundDetail>(`${base}/${group}/rounds/${encodeURIComponent(run)}`, { params: { model, page }, signal })
  ).data
}
export async function intelligenceSummary(group: number, model?: string, page = 1, signal?: AbortSignal) {
  return (await apiClient.get<IntelligenceSummary>(`${base}/${group}/summary`, { params: { model, page }, signal })).data
}
export async function intelligenceArtwork(group: number, result: number, signal?: AbortSignal) {
  return (await apiClient.get<{ output_html: string }>(`${base}/${group}/artwork/${result}`, { signal })).data.output_html
}
