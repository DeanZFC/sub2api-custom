import apiClient from '../client'
export interface PelicanReviewConfig {
  enabled: boolean
  judge_account_id: number
  judge_model: string
  daily_requests: number
}
export const defaultPelicanReviewConfig = (): PelicanReviewConfig => ({ enabled: false, judge_account_id: 0, judge_model: '', daily_requests: 200 })
export function copyQualityOperationsConfig(value: QualityOperationsConfig): QualityOperationsConfig {
  return { ...value, pelican_review: { ...defaultPelicanReviewConfig(), ...value.pelican_review } }
}
export interface QualityOperationsConfig {
  pelican_review?: PelicanReviewConfig
  version: number; budget_enabled: boolean; daily_requests: number; daily_tokens: number
  account_daily_requests: number; account_daily_tokens: number; token_reservation: number
  dynamic_enabled: boolean; healthy_rounds: number; healthy_base_seconds: number; healthy_max_seconds: number
}
export interface QualityOperationsSnapshot {
  config: QualityOperationsConfig; day: string; resets_at: string
  usage: { account_id: number; requests: number; tokens: number; unknown_requests: number }[]
}
export const qualityOperationsAPI = {
  get: async () => (await apiClient.get<QualityOperationsSnapshot>('/admin/channel-quality/operations')).data,
  save: async (config: QualityOperationsConfig) => (await apiClient.put<QualityOperationsConfig>('/admin/channel-quality/operations', config)).data,
}
