import apiClient from '../client'
export interface PelicanReviewConfig {
  enabled: boolean
  judge_account_id: number
  judge_model: string
  daily_requests: number
}
export const defaultPelicanReviewConfig = (): PelicanReviewConfig => ({ enabled: false, judge_account_id: 0, judge_model: '', daily_requests: 200 })
export function copyQualityOperationsConfig(value: QualityOperationsConfig): QualityOperationsConfig {
  return { recovery_reserve_percent: 0, ...value, pelican_review: { ...defaultPelicanReviewConfig(), ...value.pelican_review } }
}
export interface QualityOperationsConfig {
  pelican_review?: PelicanReviewConfig
  recovery_reserve_percent?: number; version: number; budget_enabled: boolean; daily_requests: number; daily_tokens: number
  account_daily_requests: number; account_daily_tokens: number; token_reservation: number
  dynamic_enabled: boolean; healthy_rounds: number; healthy_base_seconds: number; healthy_max_seconds: number
}
export interface QualitySupplier {
  id: number; version: number; name: string; account_ids: number[]; enabled: boolean
  window_seconds: number; min_accounts: number; failure_percent: number; probe_interval_seconds: number
  members: { account_id: number; name: string; kind: string; observed_at: string | null }[]
  affected: number; observed: number; incident: boolean; next_probe_at: string | null
}
export interface QualityOperationsSnapshot {
  config: QualityOperationsConfig; day: string; resets_at: string
  usage: { account_id: number; requests: number; tokens: number; unknown_requests: number; recovery_requests?: number; recovery_tokens?: number }[]
  suppliers: QualitySupplier[]
}
export const qualityOperationsAPI = {
  get: async () => (await apiClient.get<QualityOperationsSnapshot>('/admin/channel-quality/operations')).data,
  save: async (config: QualityOperationsConfig) => (await apiClient.put<QualityOperationsConfig>('/admin/channel-quality/operations', config)).data,
  saveSupplier: async (supplier: QualitySupplier) => (await (supplier.id
    ? apiClient.put<QualitySupplier>(`/admin/channel-quality/suppliers/${supplier.id}`, supplier)
    : apiClient.post<QualitySupplier>('/admin/channel-quality/suppliers', supplier))).data,
  deleteSupplier: async (supplier: QualitySupplier) => { await apiClient.delete(`/admin/channel-quality/suppliers/${supplier.id}`, { params: { version: supplier.version } }) },
}
export function defaultQualitySupplier(): QualitySupplier {
  return { id: 0, version: 0, name: '', account_ids: [], enabled: true, window_seconds: 600, min_accounts: 2, failure_percent: 60, probe_interval_seconds: 60, members: [], affected: 0, observed: 0, incident: false, next_probe_at: null }
}
