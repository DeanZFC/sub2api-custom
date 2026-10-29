import apiClient from '../client'
import type { TestPassRateSummary } from '@/types'
import type { PassiveEvidence, PassiveCacheEvidence } from './passiveQuality'

export interface QualityControl {
  source: 'active' | 'passive' | 'account'
  id: number; name: string; model: string; scope: string; phase: string; reason: string
  recovery: string; next_at?: string; updated_at: string; blocking: boolean; result_id?: number
  pass_rate?: TestPassRateSummary | null
  evidence?: PassiveEvidence; metric?: 'availability' | 'latency' | 'speed' | 'cache' | 'group_quality_target' | 'pass_rate'; allocation_percent?: number; cache?: PassiveCacheEvidence
}
export interface QualityAccount {
  id: number; name: string; platform: string; status: string; schedulable: boolean
  controls: QualityControl[]; active_plans: number; passive_policies: number
}
export interface QualityPage { items: QualityAccount[]; next_after?: number; generated_at: string }
export interface QualityFilter { search?: string; model?: string; group_id?: number; account_id?: number; problems_only?: boolean; after?: number }
export const channelQualityAPI = {
  overview: async (params: QualityFilter) => (await apiClient.get<QualityPage>('/admin/channel-quality', { params })).data,
}
