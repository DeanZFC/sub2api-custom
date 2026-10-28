import apiClient from '../client'
import type { TestPassRateSummary } from '@/types'
import type { PassiveEvidence, PassiveCacheEvidence } from './passiveQuality'

export type QualityScoreDimensionKey = 'error_rate' | 'cache_rate' | 'ttft' | 'duration' | 'tps'
export interface QualityScoreMetric { weight: number; good: number; bad: number }
export interface QualityScoreConfig {
  version: number; enabled: boolean; window_seconds: number; max_requests: number; min_samples: number; updated_at?: string
  error_rate: QualityScoreMetric; cache_rate: QualityScoreMetric; ttft: QualityScoreMetric; duration: QualityScoreMetric; tps: QualityScoreMetric
}
export interface QualityScoreDimension {
  key: QualityScoreDimensionKey; value: number | null; score: number | null; weight: number; samples: number
  status: 'ready' | 'insufficient' | 'unknown' | 'disabled'; unit: 'percent' | 'ms' | 'token/s'; reason: string
}
export interface ChannelQualityScore {
  value: number | null; status: 'disabled' | 'insufficient' | 'incomplete' | 'partial' | 'ready'
  samples: number; models: number; latest_at: string; model: string; window_seconds: number; max_requests: number; min_samples: number
  confidence: number; coverage_percent: number; dimensions: QualityScoreDimension[]
}

export interface QualityControl {
  source: 'active' | 'passive' | 'account'
  id: number; name: string; model: string; scope: string; phase: string; reason: string
  recovery: string; next_at?: string; updated_at: string; blocking: boolean; result_id?: number
  pass_rate?: TestPassRateSummary | null
  evidence?: PassiveEvidence; metric?: 'availability' | 'latency' | 'speed' | 'cache' | 'group_quality_target' | 'pass_rate'; allocation_percent?: number; cache?: PassiveCacheEvidence
}
export interface QualityAccount {
  id: number; name: string; platform: string; status: string; schedulable: boolean
  quality_score?: ChannelQualityScore
  controls: QualityControl[]; active_plans: number; passive_policies: number
}
export interface QualityPage { items: QualityAccount[]; next_after?: number; generated_at: string }
export interface QualityFilter { search?: string; model?: string; group_id?: number; account_id?: number; problems_only?: boolean; after?: number }
export const channelQualityAPI = {
  overview: async (params: QualityFilter) => (await apiClient.get<QualityPage>('/admin/channel-quality', { params })).data,
  scoreConfig: async () => (await apiClient.get<QualityScoreConfig>('/admin/channel-quality/score-config')).data,
  saveScoreConfig: async (config: QualityScoreConfig) => (await apiClient.put<QualityScoreConfig>('/admin/channel-quality/score-config', config)).data,
  scores: async (accountIds: number[], model?: string) => (await apiClient.get<Record<string, ChannelQualityScore>>('/admin/channel-quality/scores', { params: { account_ids: accountIds.join(','), ...(model ? { model } : {}) } })).data,
}
